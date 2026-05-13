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
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/repositories/pkg/controllers/repository"
	mockcache "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestRepositoryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "Repository Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "crd", "bases", "config.porch.kpt.dev_repositories.yaml")},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := runtime.NewScheme()
	err = configapi.AddToScheme(scheme)
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

// Test helper functions
func createTestRepository(name, namespace string) *configapi.Repository {
	return &configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configapi.TypeRepository.APIVersion(),
			Kind:       configapi.TypeRepository.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:   "https://github.com/example/test-repo",
				Branch: "main",
			},
		},
	}
}

func createReconcilerWithMockCache() (*repository.RepositoryReconciler, *mockcache.MockCache) {
	mockCache := mockcache.NewMockCache(GinkgoT())
	mockRepo := mockrepo.NewMockRepository(GinkgoT())

	// Setup default mock behaviors for successful sync operations
	mockCache.EXPECT().OpenRepository(mock.Anything, mock.Anything).Return(mockRepo, nil).Maybe()
	mockCache.EXPECT().CheckRepositoryConnectivity(mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCache.EXPECT().UpdateRepository(mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCache.EXPECT().CloseRepository(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Mock repository operations for sync
	mockRepo.EXPECT().Refresh(mock.Anything).Return(nil).Maybe()
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	mockRepo.EXPECT().BranchCommitHash(mock.Anything).Return("", nil).Maybe()

	reconciler := &repository.RepositoryReconciler{
		Client:                  k8sClient,
		Scheme:                  k8sClient.Scheme(),
		Cache:                   mockCache,
		HealthCheckFrequency:    2 * time.Second,
		FullSyncFrequency:       5 * time.Second,
		SyncStaleTimeout:        10 * time.Second,
		MaxConcurrentReconciles: 1,
		MaxConcurrentSyncs:      10,
	}

	// Initialize sync limiter manually instead of calling SetupWithManager
	reconciler.InitializeSyncLimiter()

	return reconciler, mockCache
}

// createMockCacheWithSlowSync creates a mock cache that simulates slow sync operations
func createMockCacheWithSlowSync() *mockcache.MockCache {
	mockCache := mockcache.NewMockCache(GinkgoT())
	mockRepo := mockrepo.NewMockRepository(GinkgoT())

	// Setup slow sync operations
	mockCache.EXPECT().OpenRepository(mock.Anything, mock.Anything).Return(mockRepo, nil).Maybe()
	mockCache.EXPECT().CheckRepositoryConnectivity(mock.Anything, mock.Anything).Return(nil).Maybe()

	// Slow refresh operation
	mockRepo.EXPECT().Refresh(mock.Anything).Run(func(ctx context.Context) {
		time.Sleep(2 * time.Second) // Simulate slow operation
	}).Return(nil).Maybe()
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	mockRepo.EXPECT().BranchCommitHash(mock.Anything).Return("", nil).Maybe()

	return mockCache
}
