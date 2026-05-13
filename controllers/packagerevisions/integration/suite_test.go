package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/controllers/packagerevision"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestPackageRevisionIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "PackageRevision Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "api", "porch", "v1alpha2", "porch.kpt.dev_packagerevisions.yaml")},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := runtime.NewScheme()
	err = porchv1alpha2.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func createTestPackageRevision(name, namespace string) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			APIVersion: porchv1alpha2.SchemeGroupVersion.String(),
			Kind:       "PackageRevision",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "test-package",
			RepositoryName: "test-repo",
			WorkspaceName:  "workspace-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}
}

func createReconcilerWithMockCache() (*packagerevision.PackageRevisionReconciler, *mockrepository.MockContentCache) {
	mockCache := mockrepository.NewMockContentCache(GinkgoT())

	reconciler := &packagerevision.PackageRevisionReconciler{
		Client:                  k8sClient,
		Scheme:                  k8sClient.Scheme(),
		ContentCache:            mockCache,
		MaxConcurrentReconciles: 1,
	}

	return reconciler, mockCache
}

func uniqueName(prefix string, counter int) string {
	return fmt.Sprintf("%s-%d", prefix, counter)
}
