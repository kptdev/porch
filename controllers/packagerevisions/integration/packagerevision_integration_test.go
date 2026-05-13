package integration

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/controllers/packagerevision"
	"github.com/kptdev/porch/pkg/repository"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
)

var _ = Describe("PackageRevision Controller Integration", func() {
	var (
		reconciler  *packagerevision.PackageRevisionReconciler
		mockCache   *mockrepository.MockContentCache
		testPR      *porchv1alpha2.PackageRevision
		nn          types.NamespacedName
		testCounter int
	)

	BeforeEach(func() {
		testCounter++
		reconciler, mockCache = createReconcilerWithMockCache()
		testPR = createTestPackageRevision(uniqueName("test-pr", testCounter), "default")
		nn = types.NamespacedName{Name: testPR.Name, Namespace: testPR.Namespace}

		Expect(k8sClient.Create(ctx, testPR)).To(Succeed())
	})

	AfterEach(func() {
		if testPR != nil {
			k8sClient.Delete(ctx, testPR)
		}
	})

	Context("When reconciling a new PackageRevision", func() {
		It("Should reconcile successfully when current matches desired", func() {
			mockContent := mockrepository.NewMockPackageContent(GinkgoT())
			mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
			mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "test-package", "workspace-1").Return(mockContent, nil)

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("Should handle not-found gracefully", func() {
			missingNN := types.NamespacedName{Name: "does-not-exist", Namespace: "default"}
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: missingNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
	})

	Context("When reconciling different lifecycle states (no-op)", func() {
		It("Should reconcile a Draft PackageRevision (no-op)", func() {
			mockContent := mockrepository.NewMockPackageContent(GinkgoT())
			mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
			mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "test-package", "workspace-1").Return(mockContent, nil)

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			var fetched porchv1alpha2.PackageRevision
			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			Expect(fetched.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))
		})

		It("Should reconcile a Proposed PackageRevision", func() {
			var fetched porchv1alpha2.PackageRevision
			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			fetched.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
			Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())

			mockContent := mockrepository.NewMockPackageContent(GinkgoT())
			mockContent.EXPECT().Lifecycle(mock.Anything).Return("Proposed")
			mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "test-package", "workspace-1").Return(mockContent, nil)

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("Should reconcile a Published PackageRevision", func() {
			var fetched porchv1alpha2.PackageRevision
			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			fetched.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())

			mockContent := mockrepository.NewMockPackageContent(GinkgoT())
			mockContent.EXPECT().Lifecycle(mock.Anything).Return("Published")
			mockContent.EXPECT().Key().Return(repository.PackageRevisionKey{}).Maybe()
			mockContent.EXPECT().GetCommitInfo().Return(time.Time{}, "").Maybe()
			mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "test-package", "workspace-1").Return(mockContent, nil)

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
	})

	Context("When performing lifecycle transitions", func() {
		type transitionCase struct {
			desired porchv1alpha2.PackageRevisionLifecycle
			current string
		}

		transitions := []transitionCase{
			{porchv1alpha2.PackageRevisionLifecycleProposed, "Draft"},
			{porchv1alpha2.PackageRevisionLifecycleDraft, "Proposed"},
			{porchv1alpha2.PackageRevisionLifecyclePublished, "Proposed"},
			{porchv1alpha2.PackageRevisionLifecycleDeletionProposed, "Published"},
			{porchv1alpha2.PackageRevisionLifecyclePublished, "DeletionProposed"},
		}

		for _, tc := range transitions {
			tc := tc
			It(fmt.Sprintf("Should transition %s -> %s and set Ready=True", tc.current, tc.desired), func() {
				var fetched porchv1alpha2.PackageRevision
				Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
				fetched.Spec.Lifecycle = tc.desired
				Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())

				repoKey := repository.RepositoryKey{Namespace: "default", Name: "test-repo"}
				mockContent := mockrepository.NewMockPackageContent(GinkgoT())
				mockContent.EXPECT().Lifecycle(mock.Anything).Return(tc.current)
				mockCache.EXPECT().GetPackageContent(mock.Anything, repoKey, "test-package", "workspace-1").Return(mockContent, nil)
				updatedContent := mockrepository.NewMockPackageContent(GinkgoT())
				updatedContent.EXPECT().Lifecycle(mock.Anything).Return(string(tc.desired)).Maybe()
				updatedContent.EXPECT().Key().Return(repository.PackageRevisionKey{}).Maybe()
				updatedContent.EXPECT().GetCommitInfo().Return(time.Time{}, "").Maybe()
				mockCache.EXPECT().UpdateLifecycle(mock.Anything, repoKey, "test-package", "workspace-1", string(tc.desired)).Return(updatedContent, nil)

				result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))

				Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
				readyCond := findCondition(fetched.Status.Conditions, porchv1alpha2.ConditionReady)
				Expect(readyCond).NotTo(BeNil())
				Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(readyCond.Reason).To(Equal(porchv1alpha2.ReasonReady))
			})
		}

		It("Should set Ready=False when UpdateLifecycle fails", func() {
			var fetched porchv1alpha2.PackageRevision
			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			fetched.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
			Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())

			repoKey := repository.RepositoryKey{Namespace: "default", Name: "test-repo"}
			mockContent := mockrepository.NewMockPackageContent(GinkgoT())
			mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
			mockCache.EXPECT().GetPackageContent(mock.Anything, repoKey, "test-package", "workspace-1").Return(mockContent, nil)
			mockCache.EXPECT().UpdateLifecycle(mock.Anything, repoKey, "test-package", "workspace-1", "Proposed").Return(nil, fmt.Errorf("git push failed"))

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			readyCond := findCondition(fetched.Status.Conditions, porchv1alpha2.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(porchv1alpha2.ReasonFailed))
			Expect(readyCond.Message).To(ContainSubstring("git push failed"))
		})

		It("Should set Ready=False when GetPackageContent fails", func() {
			repoKey := repository.RepositoryKey{Namespace: "default", Name: "test-repo"}
			mockCache.EXPECT().GetPackageContent(mock.Anything, repoKey, "test-package", "workspace-1").Return(nil, fmt.Errorf("repo not found"))

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			var fetched porchv1alpha2.PackageRevision
			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			readyCond := findCondition(fetched.Status.Conditions, porchv1alpha2.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(porchv1alpha2.ReasonFailed))
			Expect(readyCond.Message).To(ContainSubstring("repo not found"))
		})

		It("Should set Ready=True on no-op and verify condition", func() {
			mockContent := mockrepository.NewMockPackageContent(GinkgoT())
			mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
			mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "test-package", "workspace-1").Return(mockContent, nil)

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			var fetched porchv1alpha2.PackageRevision
			Expect(k8sClient.Get(ctx, nn, &fetched)).To(Succeed())
			readyCond := findCondition(fetched.Status.Conditions, porchv1alpha2.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal(porchv1alpha2.ReasonReady))
		})
	})

	Context("When SetupWithManager registers field indexes", Ordered, func() {
		var (
			mgr       manager.Manager
			mgrCtx    context.Context
			mgrCancel context.CancelFunc
		)

		BeforeAll(func() {
			var err error
			mgr, err = ctrl.NewManager(cfg, ctrl.Options{
				Scheme: k8sClient.Scheme(),
			})
			Expect(err).NotTo(HaveOccurred())

			// The manager's controller will reconcile in a goroutine.
			// Use a permissive mock that returns an error for any GetPackageContent call
			// (the controller handles errors gracefully via status conditions).
			mgrMockCache := mockrepository.NewMockContentCache(GinkgoT())
			mgrMockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(nil, fmt.Errorf("mock: no repository configured")).Maybe()

			r := &packagerevision.PackageRevisionReconciler{
				Scheme:                  mgr.GetScheme(),
				ContentCache:            mgrMockCache,
				MaxConcurrentReconciles: 1,
			}
			Expect(r.SetupWithManager(mgr)).To(Succeed())

			mgrCtx, mgrCancel = context.WithCancel(ctx)
			go func() {
				defer GinkgoRecover()
				_ = mgr.Start(mgrCtx)
			}()

			Expect(mgr.GetCache().WaitForCacheSync(ctx)).To(BeTrue())
		})

		AfterAll(func() {
			mgrCancel()
		})

		It("Should filter by lifecycle field", func() {
			var list porchv1alpha2.PackageRevisionList
			err := mgr.GetClient().List(ctx, &list, client.MatchingFields{
				string(porchv1alpha2.PkgRevSelectorLifecycle): string(porchv1alpha2.PackageRevisionLifecycleDraft),
			})
			Expect(err).NotTo(HaveOccurred())

			found := false
			for _, item := range list.Items {
				if item.Name == testPR.Name {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected to find test PR via lifecycle index")
		})

		It("Should filter by repository field", func() {
			var list porchv1alpha2.PackageRevisionList
			err := mgr.GetClient().List(ctx, &list, client.MatchingFields{
				string(porchv1alpha2.PkgRevSelectorRepository): "test-repo",
			})
			Expect(err).NotTo(HaveOccurred())

			found := false
			for _, item := range list.Items {
				if item.Name == testPR.Name {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected to find test PR via repository index")
		})

		It("Should filter by package name field", func() {
			var list porchv1alpha2.PackageRevisionList
			err := mgr.GetClient().List(ctx, &list, client.MatchingFields{
				string(porchv1alpha2.PkgRevSelectorPackageName): "test-package",
			})
			Expect(err).NotTo(HaveOccurred())

			found := false
			for _, item := range list.Items {
				if item.Name == testPR.Name {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected to find test PR via packageName index")
		})

		It("Should return empty for non-matching field values", func() {
			var list porchv1alpha2.PackageRevisionList
			err := mgr.GetClient().List(ctx, &list, client.MatchingFields{
				string(porchv1alpha2.PkgRevSelectorRepository): "nonexistent-repo",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(list.Items).To(BeEmpty())
		})

		It("Should filter by workspace name field", func() {
			var list porchv1alpha2.PackageRevisionList
			err := mgr.GetClient().List(ctx, &list, client.MatchingFields{
				string(porchv1alpha2.PkgRevSelectorWorkspaceName): "workspace-1",
			})
			Expect(err).NotTo(HaveOccurred())

			found := false
			for _, item := range list.Items {
				if item.Name == testPR.Name {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected to find test PR via workspaceName index")
		})

		It("Should find multiple PackageRevisions by same repository", func() {
			secondPR := createTestPackageRevision(fmt.Sprintf("test-pr-second-%d", testCounter), "default")
			secondPR.Spec.WorkspaceName = "workspace-2"
			Expect(k8sClient.Create(ctx, secondPR)).To(Succeed())
			defer k8sClient.Delete(ctx, secondPR)

			Eventually(func() int {
				var list porchv1alpha2.PackageRevisionList
				err := mgr.GetClient().List(ctx, &list, client.MatchingFields{
					string(porchv1alpha2.PkgRevSelectorRepository): "test-repo",
				})
				if err != nil {
					return 0
				}
				count := 0
				for _, item := range list.Items {
					if item.Name == testPR.Name || item.Name == secondPR.Name {
						count++
					}
				}
				return count
			}).Should(Equal(2))
		})
	})
})

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
