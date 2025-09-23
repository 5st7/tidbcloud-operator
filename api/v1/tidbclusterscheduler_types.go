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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SchedulerPhase defines the current phase of the scheduler
// +kubebuilder:validation:Enum=Pending;Active;Suspended;Failed
type SchedulerPhase string

const (
	SchedulerPhasePending   SchedulerPhase = "Pending"
	SchedulerPhaseActive    SchedulerPhase = "Active"
	SchedulerPhaseSuspended SchedulerPhase = "Suspended"
	SchedulerPhaseFailed    SchedulerPhase = "Failed"
)

// ClusterReference defines reference to TiDB Cloud cluster
type ClusterReference struct {
	// TiDB Cloud project ID
	// +kubebuilder:validation:Pattern=`^[0-9]+$`
	// +required
	ProjectID string `json:"projectId"`

	// TiDB Cloud cluster ID
	// +kubebuilder:validation:Pattern=`^[0-9]+$`
	// +required
	ClusterID string `json:"clusterId"`

	// Optional cluster name for display
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// Credentials reference
	// +optional
	CredentialsRef *corev1.LocalObjectReference `json:"credentialsRef,omitempty"`
}

// NodeScaleTarget defines scaling targets for a node type (general purpose)
type NodeScaleTarget struct {
	// Number of nodes (scale in/out)
	// +kubebuilder:validation:Minimum=1
	// +optional
	NodeCount *int `json:"nodeCount,omitempty"`

	// Node specification key based on actual TiDB Cloud offerings
	// Format: {vCPU}C{RAM_GB}G
	// +kubebuilder:validation:Pattern=`^(4C16G|8C16G|8C32G|8C64G|16C32G|16C64G|16C128G|32C64G|32C128G|32C256G)$`
	// +optional
	NodeSize *string `json:"nodeSize,omitempty"`

	// Storage per node in GB (scale up only, applies to TiKV/TiFlash)
	// +kubebuilder:validation:Minimum=200
	// +kubebuilder:validation:Maximum=8000
	// +optional
	Storage *int `json:"storage,omitempty"`
}

// TiKVScaleTarget defines scaling targets specifically for TiKV nodes
type TiKVScaleTarget struct {
	// Number of TiKV nodes (must be multiple of 3, minimum 3)
	// +kubebuilder:validation:Minimum=3
	// +kubebuilder:validation:MultipleOf=3
	// +optional
	NodeCount *int `json:"nodeCount,omitempty"`

	// TiKV node specification key
	// +kubebuilder:validation:Pattern=`^(4C16G|8C32G|8C64G|16C64G|32C128G)$`
	// +optional
	NodeSize *string `json:"nodeSize,omitempty"`

	// Storage per node in GB (scale up only)
	// +kubebuilder:validation:Minimum=200
	// +kubebuilder:validation:Maximum=8000
	// +optional
	Storage *int `json:"storage,omitempty"`
}

// ScaleTarget defines target configuration for scaling operations
type ScaleTarget struct {
	// TiDB node scaling
	// +optional
	TiDB *NodeScaleTarget `json:"tidb,omitempty"`

	// TiKV node scaling (must be multiple of 3)
	// +optional
	TiKV *TiKVScaleTarget `json:"tikv,omitempty"`

	// TiFlash node scaling
	// +optional
	TiFlash *NodeScaleTarget `json:"tiflash,omitempty"`
}

// SuspendSettings defines settings for suspend/resume operations
type SuspendSettings struct {
	// Force suspend even if connections exist
	// +optional
	Force bool `json:"force,omitempty"`

	// Grace period before force suspend
	// +optional
	GracePeriod *metav1.Duration `json:"gracePeriod,omitempty"`

	// Skip suspend if cluster is already scaled down
	// +optional
	SkipIfScaledDown bool `json:"skipIfScaledDown,omitempty"`
}

// Schedule defines a unified scheduling operation
// +kubebuilder:validation:XValidation:rule="(has(self.scale) && !has(self.suspend) && !has(self.resume)) || (!has(self.scale) && has(self.suspend) && !has(self.resume)) || (!has(self.scale) && !has(self.suspend) && has(self.resume))", message="exactly one of scale, suspend, or resume must be specified"
type Schedule struct {
	// Cron schedule expression (5-field format)
	// +kubebuilder:validation:Pattern=`^(@(annually|yearly|monthly|weekly|daily|hourly))|(@every (\d+(ns|us|µs|ms|s|m|h))+)|(((\*|(\d+,)*\d+|(\d+(\-|\/)\d+)|\*\/\d+) ){4}(\*|(\d+,)*\d+|(\d+(\-|\/)\d+)|\*\/\d+))$`
	// +required
	Schedule string `json:"schedule"`

	// Human-readable description
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Description string `json:"description,omitempty"`

	// Scale operation - if present, performs scaling
	// +optional
	Scale *ScaleTarget `json:"scale,omitempty"`

	// Suspend operation - if true, suspends the cluster
	// +optional
	Suspend *bool `json:"suspend,omitempty"`

	// Resume operation - if true, resumes the cluster
	// +optional
	Resume *bool `json:"resume,omitempty"`

	// Settings for suspend/resume operations
	// +optional
	Settings SuspendSettings `json:"settings,omitempty"`
}

// RetryConfig defines retry configuration for failed operations
type RetryConfig struct {
	// Maximum retry attempts
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:default=5
	// +optional
	MaxRetries int `json:"maxRetries,omitempty"`

	// Initial retry delay
	// +kubebuilder:default="1m"
	// +optional
	InitialDelay *metav1.Duration `json:"initialDelay,omitempty"`

	// Backoff multiplier for retries (as percentage, 200 = 2.0x)
	// +kubebuilder:validation:Minimum=100
	// +kubebuilder:validation:Maximum=500
	// +kubebuilder:default=200
	// +optional
	BackoffMultiplier int `json:"backoffMultiplier,omitempty"`

	// Maximum retry delay
	// +kubebuilder:default="16m"
	// +optional
	MaxDelay *metav1.Duration `json:"maxDelay,omitempty"`
}

// SchedulerSettings defines global configuration for scheduler behavior
type SchedulerSettings struct {
	// Maximum concurrent scaling operations
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:default=3
	// +optional
	ConcurrencyLimit int `json:"concurrencyLimit,omitempty"`

	// Default timeout for scaling operations
	// +kubebuilder:default="30m"
	// +optional
	OperationTimeout *metav1.Duration `json:"operationTimeout,omitempty"`

	// Retry configuration for failed operations
	// +optional
	RetryConfig RetryConfig `json:"retryConfig,omitempty"`
}

// TiDBClusterSchedulerSpec defines the desired state of TiDBClusterScheduler
type TiDBClusterSchedulerSpec struct {
	// Target cluster configuration
	// +required
	ClusterRef ClusterReference `json:"clusterRef"`

	// Timezone for schedule interpretation (IANA format)
	// +kubebuilder:validation:Pattern=`^[A-Za-z_]+(/[A-Za-z_]+)*$`
	// +kubebuilder:default="UTC"
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// Unified schedules for all operations (scale, suspend, resume)
	// +optional
	Schedules []Schedule `json:"schedules,omitempty"`

	// Global settings
	// +optional
	Settings SchedulerSettings `json:"settings,omitempty"`
}

// ScheduleStatus defines execution status of individual schedules
type ScheduleStatus struct {
	// Schedule expression
	// +required
	Schedule string `json:"schedule"`

	// Cron job ID for tracking
	// +required
	JobID int `json:"jobId"`

	// Next scheduled execution
	// +optional
	NextRun *metav1.Time `json:"nextRun,omitempty"`

	// Last execution time
	// +optional
	LastRun *metav1.Time `json:"lastRun,omitempty"`

	// Result of last execution
	// +optional
	LastResult string `json:"lastResult,omitempty"`

	// Total executions
	// +optional
	ExecutionCount int `json:"executionCount,omitempty"`
}

// FailedOperation defines failed scaling operations for retry
type FailedOperation struct {
	// Original schedule
	// +required
	Schedule string `json:"schedule"`

	// Operation that failed
	// +required
	Operation string `json:"operation"`

	// Failure timestamp
	// +required
	FailureTime metav1.Time `json:"failureTime"`

	// Error message
	// +required
	Error string `json:"error"`

	// Retry attempt count
	// +required
	RetryCount int `json:"retryCount"`

	// Next retry time
	// +optional
	NextRetry *metav1.Time `json:"nextRetry,omitempty"`

	// Maximum retry attempts
	// +optional
	MaxRetries int `json:"maxRetries,omitempty"`
}

// SchedulerConditionType defines the type of conditions
type SchedulerConditionType string

const (
	// ConditionReady indicates the scheduler is ready and operational
	ConditionReady SchedulerConditionType = "Ready"
	// ConditionProgressing indicates the scheduler is making progress
	ConditionProgressing SchedulerConditionType = "Progressing"
	// ConditionDegraded indicates the scheduler has issues but is still operational
	ConditionDegraded SchedulerConditionType = "Degraded"
)

// SchedulerCondition defines scheduler status conditions
type SchedulerCondition struct {
	// Type of condition
	Type SchedulerConditionType `json:"type"`

	// Status of the condition
	Status metav1.ConditionStatus `json:"status"`

	// Reason for the condition
	Reason string `json:"reason,omitempty"`

	// Message describing the condition
	Message string `json:"message,omitempty"`

	// Last time condition transitioned
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
}

// TiDBClusterSchedulerStatus defines the observed state of TiDBClusterScheduler
type TiDBClusterSchedulerStatus struct {
	// Current phase
	// +optional
	Phase SchedulerPhase `json:"phase,omitempty"`

	// Status conditions
	// +optional
	Conditions []SchedulerCondition `json:"conditions,omitempty"`

	// Currently active schedules
	// +optional
	ActiveSchedules []ScheduleStatus `json:"activeSchedules,omitempty"`

	// Timestamp of last execution
	// +optional
	LastExecution *metav1.Time `json:"lastExecution,omitempty"`

	// Failed operations awaiting retry
	// +optional
	FailedOperations []FailedOperation `json:"failedOperations,omitempty"`

	// Most recently observed generation
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TiDBClusterScheduler is the Schema for the tidbclusterschedulers API
type TiDBClusterScheduler struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of TiDBClusterScheduler
	// +required
	Spec TiDBClusterSchedulerSpec `json:"spec"`

	// status defines the observed state of TiDBClusterScheduler
	// +optional
	Status TiDBClusterSchedulerStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// TiDBClusterSchedulerList contains a list of TiDBClusterScheduler
type TiDBClusterSchedulerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TiDBClusterScheduler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TiDBClusterScheduler{}, &TiDBClusterSchedulerList{})
}
