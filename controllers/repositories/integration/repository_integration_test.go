// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/repositories/pkg/controllers/repository"
)

var _ = Describe("Repository Controller Integration", func() {
	var (
		reconciler     *repository.RepositoryReconciler
		testRepo       *configapi.Repository
		namespacedName types.NamespacedName
		testCounter    int
	)

	BeforeEach(func() {
		testCounter++
		reconciler, _ = createReconcilerWithMockCache()
		testRepo = createTestRepository(fmt.Sprintf("test-repo-%d", testCounter), "default")
		namespacedName = types.NamespacedName{
			Name:      testRepo.Name,
			Namespace: testRepo.Namespace,
		}

		Expect(k8sClient.Create(ctx, testRepo)).To(Succeed())
	})

	AfterEach(func() {
		if testRepo != nil {
			k8sClient.Delete(ctx, testRepo)
		}
	})

	Context("When reconciling a new repository", func() {
		It("Should add finalizer and perform full sync on first reconcile", func() {
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(2 * time.Second))

			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			Expect(updatedRepo.Finalizers).To(ContainElement(repository.RepositoryFinalizer))

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
				for _, condition := range updatedRepo.Status.Conditions {
					if condition.Type == configapi.RepositoryReady && condition.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*10, time.Millisecond*200).Should(BeTrue())
		})

		It("Should handle sync capacity exceeded", func() {
			lowCapacityReconciler := &repository.RepositoryReconciler{
				Client:               k8sClient,
				Scheme:               k8sClient.Scheme(),
				Cache:                createMockCacheWithSlowSync(),
				HealthCheckFrequency: 2 * time.Second,
				FullSyncFrequency:    5 * time.Second,
				SyncStaleTimeout:     10 * time.Second,
				MaxConcurrentSyncs:   1,
			}
			lowCapacityReconciler.InitializeSyncLimiter()

			_, err := lowCapacityReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			secondRepo := createTestRepository(fmt.Sprintf("test-repo-capacity-%d", testCounter), "default")
			Expect(k8sClient.Create(ctx, secondRepo)).To(Succeed())
			defer k8sClient.Delete(ctx, secondRepo)

			secondNamespacedName := types.NamespacedName{
				Name:      secondRepo.Name,
				Namespace: secondRepo.Namespace,
			}

			result, err := lowCapacityReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: secondNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		})
	})

	Context("When reconciling an existing repository", func() {
		BeforeEach(func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(100 * time.Millisecond)
		})

		It("Should complete async full sync and update status", func() {
			Eventually(func() bool {
				updatedRepo := &configapi.Repository{}
				if err := k8sClient.Get(ctx, namespacedName, updatedRepo); err != nil {
					return false
				}
				return updatedRepo.Status.LastFullSyncTime != nil
			}, time.Second*10, time.Millisecond*200).Should(BeTrue())

			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			Expect(updatedRepo.Status.Conditions).NotTo(BeEmpty())
		})

		It("Should update status after multiple reconciles", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				updatedRepo := &configapi.Repository{}
				if err := k8sClient.Get(ctx, namespacedName, updatedRepo); err != nil {
					return false
				}
				for _, condition := range updatedRepo.Status.Conditions {
					if condition.Type == configapi.RepositoryReady && condition.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*10, time.Millisecond*200).Should(BeTrue())
		})

		It("Should perform health check when no full sync needed", func() {
			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())

			now := metav1.Now()
			updatedRepo.Status.LastFullSyncTime = &now
			updatedRepo.Status.Conditions = []metav1.Condition{{
				Type:               configapi.RepositoryReady,
				Status:             metav1.ConditionTrue,
				Reason:             configapi.ReasonReady,
				Message:            "Repository is ready",
				ObservedGeneration: updatedRepo.Generation,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-1 * time.Second)),
			}}
			Expect(k8sClient.Status().Update(ctx, updatedRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically("<=", 2*time.Second))
		})

		It("Should perform full sync when spec changes", func() {
			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			updatedRepo.Spec.Sync = &configapi.RepositorySync{
				Schedule: "0 */2 * * *",
			}
			Expect(k8sClient.Update(ctx, updatedRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(2 * time.Second))
		})
	})

	Context("When handling repository deletion", func() {
		BeforeEach(func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(100 * time.Millisecond)
		})

		It("Should clean up resources when repository is deleted", func() {
			Expect(k8sClient.Delete(ctx, testRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

			Eventually(func() bool {
				updatedRepo := &configapi.Repository{}
				err := k8sClient.Get(ctx, namespacedName, updatedRepo)
				return err != nil
			}, time.Second*5, time.Millisecond*100).Should(BeTrue())
		})
	})

	Context("When testing sync scheduling and intervals", func() {
		It("Should calculate correct sync intervals for cron schedules", func() {
			testRepo.Spec.Sync = &configapi.RepositorySync{
				Schedule: "0 */1 * * *",
			}
			Expect(k8sClient.Update(ctx, testRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(2 * time.Second))
		})

		It("Should use default frequency for invalid cron", func() {
			testRepo.Spec.Sync = &configapi.RepositorySync{
				Schedule: "invalid-cron",
			}
			Expect(k8sClient.Update(ctx, testRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(2 * time.Second))
		})

		It("Should handle RunOnceAt scheduled sync", func() {
			futureTime := metav1.NewTime(time.Now().Add(5 * time.Second))
			testRepo.Spec.Sync = &configapi.RepositorySync{
				RunOnceAt: &futureTime,
			}
			Expect(k8sClient.Update(ctx, testRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})
	})

	Context("When testing error recovery scenarios", func() {
		It("Should handle RunOnceAt not yet due", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(100 * time.Millisecond)

			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			futureTime := metav1.NewTime(time.Now().Add(10 * time.Minute))
			updatedRepo.Spec.Sync = &configapi.RepositorySync{
				RunOnceAt: &futureTime,
			}
			updatedRepo.Status.ObservedGeneration = updatedRepo.Generation - 1
			Expect(k8sClient.Update(ctx, updatedRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})

		It("Should handle past RunOnceAt time", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(100 * time.Millisecond)

			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			pastTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
			updatedRepo.Spec.Sync = &configapi.RepositorySync{
				RunOnceAt: &pastTime,
			}
			Expect(k8sClient.Update(ctx, updatedRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(2 * time.Second))
		})
	})

	Context("When testing sync in progress detection", func() {
		BeforeEach(func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(200 * time.Millisecond)
		})

		It("Should skip operations when full sync is in progress", func() {
			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			updatedRepo.Status.Conditions = []metav1.Condition{{
				Type:               configapi.RepositoryReady,
				Status:             metav1.ConditionFalse,
				Reason:             configapi.ReasonReconciling,
				Message:            "Sync in progress",
				ObservedGeneration: updatedRepo.Generation,
				LastTransitionTime: metav1.Now(),
			}}
			Expect(k8sClient.Status().Update(ctx, updatedRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
			Expect(result.Requeue).To(BeTrue())
		})

		It("Should detect stale sync and retry", func() {
			updatedRepo := &configapi.Repository{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedRepo)).To(Succeed())
			updatedRepo.Status.Conditions = []metav1.Condition{{
				Type:               configapi.RepositoryReady,
				Status:             metav1.ConditionFalse,
				Reason:             configapi.ReasonReconciling,
				Message:            "Sync in progress",
				ObservedGeneration: updatedRepo.Generation,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-15 * time.Minute)),
			}}
			Expect(k8sClient.Status().Update(ctx, updatedRepo)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(2 * time.Second))
		})
	})

})
