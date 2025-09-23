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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulerv1 "github.com/5st7/tidbcloud-operator/api/v1"
)

var _ = Describe("TiDBClusterScheduler Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		tidbclusterscheduler := &schedulerv1.TiDBClusterScheduler{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind TiDBClusterScheduler")
			err := k8sClient.Get(ctx, typeNamespacedName, tidbclusterscheduler)
			if err != nil && errors.IsNotFound(err) {
				resource := &schedulerv1.TiDBClusterScheduler{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: schedulerv1.TiDBClusterSchedulerSpec{
						ClusterRef: schedulerv1.ClusterReference{
							ProjectID:   "1372813089454558678",
							ClusterID:   "10417002845199914497",
							ClusterName: "test-cluster",
						},
						Timezone: "UTC",
						Schedules: []schedulerv1.Schedule{
							{
								Schedule:    "0 8 * * 1-5",
								Description: "Test scale up weekdays",
								Scale: &schedulerv1.ScaleTarget{
									TiDB: &schedulerv1.NodeScaleTarget{
										NodeCount: intPtr(2),
										NodeSize:  stringPtr("4C16G"),
									},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &schedulerv1.TiDBClusterScheduler{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance TiDBClusterScheduler")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &TiDBClusterSchedulerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

// Helper functions
func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
