/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	schedulerv1 "github.com/5st7/tidbcloud-operator/api/v1"
	"github.com/5st7/tidbcloud-operator/internal/services/scheduler"
	"github.com/5st7/tidbcloud-operator/internal/services/tidbcloud"
)

// TiDBClusterSchedulerReconciler reconciles a TiDBClusterScheduler object
type TiDBClusterSchedulerReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	SchedulerService scheduler.SchedulerService
}

// +kubebuilder:rbac:groups=scheduler.tsurai.jp,resources=tidbclusterschedulers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduler.tsurai.jp,resources=tidbclusterschedulers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scheduler.tsurai.jp,resources=tidbclusterschedulers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TiDBClusterSchedulerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the TiDBClusterScheduler instance
	scheduler := &schedulerv1.TiDBClusterScheduler{}
	err := r.Get(ctx, req.NamespacedName, scheduler)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Remove all schedules for this scheduler
			if err := r.SchedulerService.RemoveAllSchedules(req.Namespace, req.Name); err != nil {
				log.Error(err, "Failed to remove schedules for deleted scheduler")
			}
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request
		return ctrl.Result{}, err
	}

	// Set up finalizer for cleanup
	const finalizerName = "scheduler.tsurai.jp/cleanup"
	if scheduler.ObjectMeta.DeletionTimestamp.IsZero() {
		// Object is not being deleted, add finalizer if not present
		if !controllerutil.ContainsFinalizer(scheduler, finalizerName) {
			controllerutil.AddFinalizer(scheduler, finalizerName)
			return r.updateScheduler(ctx, scheduler)
		}
	} else {
		// Object is being deleted
		if controllerutil.ContainsFinalizer(scheduler, finalizerName) {
			// Remove all schedules before allowing deletion
			if err := r.SchedulerService.RemoveAllSchedules(scheduler.Namespace, scheduler.Name); err != nil {
				log.Error(err, "Failed to cleanup schedules during deletion")
				return ctrl.Result{}, err
			}

			// Remove finalizer to allow deletion
			controllerutil.RemoveFinalizer(scheduler, finalizerName)
			return r.updateScheduler(ctx, scheduler)
		}
		return ctrl.Result{}, nil
	}

	// Validate cluster reference and connectivity
	tidbClient, err := r.createTiDBCloudClient(ctx, scheduler)
	if err != nil {
		return r.updateStatus(ctx, scheduler, schedulerv1.SchedulerCondition{
			Type:    schedulerv1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "CredentialError",
			Message: fmt.Sprintf("failed to create TiDB Cloud client: %v", err),
		})
	}

	if err := r.validateClusterAccess(ctx, scheduler, tidbClient); err != nil {
		return r.updateStatus(ctx, scheduler, schedulerv1.SchedulerCondition{
			Type:    schedulerv1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ClusterValidationFailed",
			Message: err.Error(),
		})
	}

	// Reconcile unified schedules
	if err := r.reconcileSchedules(ctx, scheduler, tidbClient); err != nil {
		log.Error(err, "Failed to reconcile schedules")
		return r.updateStatus(ctx, scheduler, schedulerv1.SchedulerCondition{
			Type:    schedulerv1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ScheduleError",
			Message: err.Error(),
		})
	}

	// Update status to ready
	return r.updateStatus(ctx, scheduler, schedulerv1.SchedulerCondition{
		Type:    schedulerv1.ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "SchedulesActive",
		Message: "All schedules are active and healthy",
	})
}

// createTiDBCloudClient creates a TiDB Cloud client based on scheduler's credentials
func (r *TiDBClusterSchedulerReconciler) createTiDBCloudClient(ctx context.Context, scheduler *schedulerv1.TiDBClusterScheduler) (tidbcloud.Client, error) {
	log := logf.FromContext(ctx)

	// Use credentials from secret reference if specified
	if scheduler.Spec.ClusterRef.CredentialsRef != nil {
		log.Info("Creating TiDB Cloud client from secret reference",
			"secretName", scheduler.Spec.ClusterRef.CredentialsRef.Name,
			"namespace", scheduler.Namespace)
		return r.createClientFromSecret(ctx, scheduler)
	}

	// Fallback to environment variables (current behavior)
	log.Info("No credentialsRef specified, falling back to environment variables")
	return r.createClientFromEnv()
}

// createClientFromSecret creates client from Kubernetes secret
func (r *TiDBClusterSchedulerReconciler) createClientFromSecret(ctx context.Context, scheduler *schedulerv1.TiDBClusterScheduler) (tidbcloud.Client, error) {
	log := logf.FromContext(ctx)
	secretName := scheduler.Spec.ClusterRef.CredentialsRef.Name
	secret := &corev1.Secret{}

	log.Info("Fetching credentials from secret", "secretName", secretName, "namespace", scheduler.Namespace)

	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: scheduler.Namespace,
	}, secret)
	if err != nil {
		log.Error(err, "Failed to get credentials secret", "secretName", secretName)
		return nil, fmt.Errorf("failed to get credentials secret %s: %w", secretName, err)
	}

	publicKey, ok := secret.Data["publicKey"]
	if !ok {
		log.Error(nil, "publicKey not found in secret", "secretName", secretName)
		return nil, fmt.Errorf("publicKey not found in secret %s", secretName)
	}

	privateKey, ok := secret.Data["privateKey"]
	if !ok {
		log.Error(nil, "privateKey not found in secret", "secretName", secretName)
		return nil, fmt.Errorf("privateKey not found in secret %s", secretName)
	}

	log.Info("Successfully retrieved credentials from secret",
		"secretName", secretName,
		"publicKey", string(publicKey),
		"privateKeyLength", len(privateKey))

	config := &tidbcloud.Config{
		BaseURL:           "https://api.tidbcloud.com/api/v1beta1",
		PublicKey:         string(publicKey),
		PrivateKey:        string(privateKey),
		Timeout:           30 * time.Second,
		RequestsPerMinute: 100,
	}

	log.Info("Creating TiDB Cloud client with digest authentication")
	client, err := tidbcloud.NewClient(config)
	if err != nil {
		log.Error(err, "Failed to create TiDB Cloud client")
		return nil, err
	}

	log.Info("TiDB Cloud client created successfully with digest authentication")
	return client, nil
}

// createClientFromEnv creates client from environment variables
func (r *TiDBClusterSchedulerReconciler) createClientFromEnv() (tidbcloud.Client, error) {
	// This will use environment variables from the pod
	// Note: This is a fallback for backward compatibility
	return nil, fmt.Errorf("environment-based authentication not supported in per-scheduler mode, please specify credentialsRef")
}

// validateClusterAccess validates that we can access the TiDB Cloud cluster
func (r *TiDBClusterSchedulerReconciler) validateClusterAccess(ctx context.Context, scheduler *schedulerv1.TiDBClusterScheduler, client tidbcloud.Client) error {
	log := logf.FromContext(ctx)
	projectID := scheduler.Spec.ClusterRef.ProjectID
	clusterID := scheduler.Spec.ClusterRef.ClusterID

	log.Info("Validating cluster access", "projectID", projectID, "clusterID", clusterID)

	cluster, err := client.GetCluster(ctx, projectID, clusterID)
	if err != nil {
		log.Error(err, "Failed to access cluster", "projectID", projectID, "clusterID", clusterID)
		return fmt.Errorf("failed to access cluster %s in project %s: %w", clusterID, projectID, err)
	}

	log.Info("Successfully validated cluster access",
		"projectID", projectID,
		"clusterID", clusterID,
		"clusterName", cluster.Name,
		"clusterStatus", cluster.Status)
	return nil
}

// reconcileSchedules manages unified schedules
func (r *TiDBClusterSchedulerReconciler) reconcileSchedules(ctx context.Context, scheduler *schedulerv1.TiDBClusterScheduler, tidbClient tidbcloud.Client) error {
	log := logf.FromContext(ctx)

	// Setup timezone context
	timezone := "UTC"
	if scheduler.Spec.Timezone != "" {
		timezone = scheduler.Spec.Timezone
	}
	ctx = context.WithValue(ctx, "timezone", timezone)

	// Remove existing schedules
	if err := r.SchedulerService.RemoveAllSchedules(scheduler.Namespace, scheduler.Name); err != nil {
		return fmt.Errorf("failed to remove existing schedules: %w", err)
	}

	// Add new schedules
	for i, schedule := range scheduler.Spec.Schedules {
		if schedule.Scale != nil {
			// Scale operation
			callback := r.createScaleCallback(scheduler, schedule, tidbClient)
			jobID, err := r.SchedulerService.AddSchedule(ctx, scheduler.Namespace, scheduler.Name, schedule, callback)
			if err != nil {
				return fmt.Errorf("failed to add scale schedule %d: %w", i, err)
			}
			log.Info("Added scale schedule", "schedule", schedule.Schedule, "description", schedule.Description, "jobID", jobID)

		} else if schedule.Suspend != nil && *schedule.Suspend {
			// Suspend operation
			callback := r.createSuspendCallback(scheduler, schedule, tidbClient)
			jobID, err := r.SchedulerService.AddSchedule(ctx, scheduler.Namespace, scheduler.Name, schedule, callback)
			if err != nil {
				return fmt.Errorf("failed to add suspend schedule %d: %w", i, err)
			}
			log.Info("Added suspend schedule", "schedule", schedule.Schedule, "description", schedule.Description, "jobID", jobID)

		} else if schedule.Resume != nil && *schedule.Resume {
			// Resume operation
			callback := r.createResumeCallback(scheduler, schedule, tidbClient)
			jobID, err := r.SchedulerService.AddSchedule(ctx, scheduler.Namespace, scheduler.Name, schedule, callback)
			if err != nil {
				return fmt.Errorf("failed to add resume schedule %d: %w", i, err)
			}
			log.Info("Added resume schedule", "schedule", schedule.Schedule, "description", schedule.Description, "jobID", jobID)
		}
	}

	return nil
}

// createScaleCallback creates a callback function for scale operations
func (r *TiDBClusterSchedulerReconciler) createScaleCallback(scheduler *schedulerv1.TiDBClusterScheduler, schedule schedulerv1.Schedule, tidbClient tidbcloud.Client) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		log := logf.Log.WithValues("scheduler", scheduler.Name, "namespace", scheduler.Namespace)
		log.Info("Executing scale operation", "schedule", schedule.Schedule, "description", schedule.Description)

		// Create scale request
		scaleReq := &tidbcloud.ScaleRequest{}

		if schedule.Scale.TiDB != nil && schedule.Scale.TiDB.NodeCount != nil {
			scaleReq.TiDBNodeSetting = &tidbcloud.NodeConfiguration{
				NodeQuantity: *schedule.Scale.TiDB.NodeCount,
			}
			if schedule.Scale.TiDB.NodeSize != nil {
				scaleReq.TiDBNodeSetting.NodeSize = *schedule.Scale.TiDB.NodeSize
			}
		}

		if schedule.Scale.TiKV != nil && schedule.Scale.TiKV.NodeCount != nil {
			scaleReq.TiKVNodeSetting = &tidbcloud.NodeConfiguration{
				NodeQuantity: *schedule.Scale.TiKV.NodeCount,
			}
			if schedule.Scale.TiKV.NodeSize != nil {
				scaleReq.TiKVNodeSetting.NodeSize = *schedule.Scale.TiKV.NodeSize
			}
			if schedule.Scale.TiKV.Storage != nil {
				scaleReq.TiKVNodeSetting.Storage = *schedule.Scale.TiKV.Storage
			}
		}

		if schedule.Scale.TiFlash != nil && schedule.Scale.TiFlash.NodeCount != nil {
			scaleReq.TiFlashNodeSetting = &tidbcloud.NodeConfiguration{
				NodeQuantity: *schedule.Scale.TiFlash.NodeCount,
			}
			if schedule.Scale.TiFlash.NodeSize != nil {
				scaleReq.TiFlashNodeSetting.NodeSize = *schedule.Scale.TiFlash.NodeSize
			}
			if schedule.Scale.TiFlash.Storage != nil {
				scaleReq.TiFlashNodeSetting.Storage = *schedule.Scale.TiFlash.Storage
			}
		}

		// Execute scale operation
		err := tidbClient.ScaleCluster(
			ctx,
			scheduler.Spec.ClusterRef.ProjectID,
			scheduler.Spec.ClusterRef.ClusterID,
			scaleReq,
		)
		if err != nil {
			log.Error(err, "Scale operation failed")
		} else {
			log.Info("Scale operation completed successfully")
		}
	}
}

// createSuspendCallback creates a callback function for suspend operations
func (r *TiDBClusterSchedulerReconciler) createSuspendCallback(scheduler *schedulerv1.TiDBClusterScheduler, schedule schedulerv1.Schedule, tidbClient tidbcloud.Client) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		log := logf.Log.WithValues("scheduler", scheduler.Name, "namespace", scheduler.Namespace)
		log.Info("Executing suspend operation", "schedule", schedule.Schedule, "description", schedule.Description)

		err := tidbClient.SuspendCluster(
			ctx,
			scheduler.Spec.ClusterRef.ProjectID,
			scheduler.Spec.ClusterRef.ClusterID,
		)
		if err != nil {
			log.Error(err, "Suspend operation failed")
		} else {
			log.Info("Suspend operation completed successfully")
		}
	}
}

// createResumeCallback creates a callback function for resume operations
func (r *TiDBClusterSchedulerReconciler) createResumeCallback(scheduler *schedulerv1.TiDBClusterScheduler, schedule schedulerv1.Schedule, tidbClient tidbcloud.Client) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		log := logf.Log.WithValues("scheduler", scheduler.Name, "namespace", scheduler.Namespace)
		log.Info("Executing resume operation", "schedule", schedule.Schedule, "description", schedule.Description)

		err := tidbClient.ResumeCluster(
			ctx,
			scheduler.Spec.ClusterRef.ProjectID,
			scheduler.Spec.ClusterRef.ClusterID,
		)
		if err != nil {
			log.Error(err, "Resume operation failed")
		} else {
			log.Info("Resume operation completed successfully")
		}
	}
}

// updateScheduler updates the scheduler resource
func (r *TiDBClusterSchedulerReconciler) updateScheduler(ctx context.Context, scheduler *schedulerv1.TiDBClusterScheduler) (ctrl.Result, error) {
	err := r.Update(ctx, scheduler)
	return ctrl.Result{}, err
}

// updateStatus updates the scheduler status with a condition
func (r *TiDBClusterSchedulerReconciler) updateStatus(ctx context.Context, scheduler *schedulerv1.TiDBClusterScheduler, condition schedulerv1.SchedulerCondition) (ctrl.Result, error) {
	condition.LastTransitionTime = metav1.NewTime(time.Now())

	// Find existing condition and update it, or append new condition
	found := false
	for i, existing := range scheduler.Status.Conditions {
		if existing.Type == condition.Type {
			scheduler.Status.Conditions[i] = condition
			found = true
			break
		}
	}
	if !found {
		scheduler.Status.Conditions = append(scheduler.Status.Conditions, condition)
	}

	err := r.Status().Update(ctx, scheduler)
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *TiDBClusterSchedulerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&schedulerv1.TiDBClusterScheduler{}).
		Named("tidbclusterscheduler").
		Complete(r)
}
