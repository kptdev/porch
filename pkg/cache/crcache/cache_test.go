// Copyright 2022, 2024-2025 The kpt Authors
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

package crcache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/crcache/meta"
	fakemeta "github.com/kptdev/porch/pkg/cache/crcache/meta/fake"
	fakecache "github.com/kptdev/porch/pkg/cache/fake"
	"github.com/kptdev/porch/pkg/cache/repomap"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	gitserver "github.com/kptdev/porch/test/git/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func TestLatestPackages(t *testing.T) {
	ctx := context.Background()
	testPath := filepath.Join("..", "..", "externalrepo", "git", "testdata")

	cachedRepo := openRepositoryFromArchive(t, ctx, testPath, "nested")

	wantLatest := map[string]int{
		"sample":                    2,
		"catalog/empty":             1,
		"catalog/gcp/bucket":        1,
		"catalog/namespace/basens":  3,
		"catalog/namespace/istions": 3,
	}
	revisions, err := cachedRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	gotLatest := map[string]int{}
	for _, pr := range revisions {
		rev, err := pr.GetPackageRevision(ctx)
		if err != nil {
			t.Errorf("didn't expect error, but got %v", err)
		}

		if latest, ok := rev.Labels[porchapi.LatestPackageRevisionKey]; ok {
			if got, want := latest, porchapi.LatestPackageRevisionValue; got != want {
				t.Errorf("%s label value: got %q, want %q", porchapi.LatestPackageRevisionKey, got, want)
				continue
			}

			if existing, ok := gotLatest[rev.Spec.PackageName]; ok {
				t.Errorf("Multiple latest package revisions for package %q: %q and %q",
					rev.Spec.PackageName, rev.Spec.Revision, existing)
			}

			// latest package
			gotLatest[rev.Spec.PackageName] = rev.Spec.Revision
		}
	}

	if !cmp.Equal(wantLatest, gotLatest) {
		t.Errorf("Latest package revisions differ (-want,+got): %s", cmp.Diff(wantLatest, gotLatest))
	}
}

func TestPublishedLatest(t *testing.T) {
	ctx := context.Background()
	testPath := filepath.Join("..", "..", "externalrepo", "git", "testdata")
	cachedRepo := openRepositoryFromArchive(t, ctx, testPath, "nested")

	revisions, err := cachedRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Path:    "catalog/gcp",
				Package: "bucket",
			},
			WorkspaceName: "v2",
		},
	})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	// Expect a single result
	if got, want := len(revisions), 1; got != want {
		t.Fatalf("ListPackageRevisions returned %d packages; want %d", got, want)
	}

	bucket := revisions[0]
	// Expect draft package
	if got, want := bucket.Lifecycle(ctx), porchapi.PackageRevisionLifecycleDraft; got != want {
		t.Fatalf("Bucket package lifecycle: got %s, want %s", got, want)
	}

	update, err := cachedRepo.UpdatePackageRevision(ctx, bucket)
	if err != nil {
		t.Fatalf("UpdatePackaeg(%s) failed: %v", bucket.Key(), err)
	}
	if err := update.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished); err != nil {
		t.Fatalf("UpdateLifecycle failed; %v", err)
	}
	closed, err := cachedRepo.ClosePackageRevisionDraft(ctx, update, 0)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	resource, err := closed.GetPackageRevision(ctx)
	if err != nil {
		t.Errorf("didn't expect error, but got %v", err)
	}
	if got, ok := resource.Labels[porchapi.LatestPackageRevisionKey]; !ok {
		t.Errorf("Label %s not found as expected", porchapi.LatestPackageRevisionKey)
	} else if want := porchapi.LatestPackageRevisionValue; got != want {
		t.Errorf("Latest label: got %s, want %s", got, want)
	}
}

func TestDeletePublishedMain(t *testing.T) {
	ctx := context.Background()
	testPath := filepath.Join("../..", "externalrepo", "git", "testdata")
	cachedRepo := openRepositoryFromArchive(t, ctx, testPath, "nested")

	revisions, err := cachedRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Path:    "catalog/gcp",
				Package: "bucket",
			},
			WorkspaceName: "v2",
		},
	})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	// Expect a single result
	if got, want := len(revisions), 1; got != want {
		t.Fatalf("ListPackageRevisions returned %d packages; want %d", got, want)
	}

	bucket := revisions[0]
	// Expect draft package
	if got, want := bucket.Lifecycle(ctx), porchapi.PackageRevisionLifecycleDraft; got != want {
		t.Fatalf("Bucket package lifecycle: got %s, want %s", got, want)
	}

	update, err := cachedRepo.UpdatePackageRevision(ctx, bucket)
	if err != nil {
		t.Fatalf("UpdatePackage(%s) failed: %v", bucket.Key(), err)
	}
	if err := update.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished); err != nil {
		t.Fatalf("UpdateLifecycle failed; %v", err)
	}
	closed, err := cachedRepo.ClosePackageRevisionDraft(ctx, update, 0)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	_, err = closed.GetPackageRevision(ctx)
	if err != nil {
		t.Errorf("didn't expect error, but got %v", err)
	}

	publishedRevisions, err := cachedRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Path:    "catalog/gcp",
				Package: "bucket",
			},
			WorkspaceName: "main",
			Revision:      -1,
		},
		Lifecycles: []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished},
	})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	// Expect a single result
	if got, want := len(publishedRevisions), 1; got != want {
		t.Fatalf("ListPackageRevisions returned %d packages; want %d", got, want)
	}

	approvedBucket := publishedRevisions[0]

	if got, want := approvedBucket.Lifecycle(ctx), porchapi.PackageRevisionLifecyclePublished; got != want {
		t.Fatalf("Approved Bucket package lifecycle: got %s, want %s", got, want)
	}

	err = approvedBucket.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleDeletionProposed)
	if err != nil {
		t.Fatalf("Deletion proposal for approved Bucket failed; %v", err)
	}
	err = cachedRepo.DeletePackageRevision(ctx, approvedBucket)
	if err != nil {
		t.Fatalf("Deleting Main packageRevision failed; %v", err)
	}

	postDeletePublishedRevisions, err := cachedRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Path:    "catalog/gcp",
				Package: "bucket",
			},
			WorkspaceName: "main",
			Revision:      -1,
		},
		Lifecycles: []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished},
	})

	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	//Expect 0 entries
	if got, want := len(postDeletePublishedRevisions), 0; got != want {
		t.Fatalf("ListPackageRevisions returned %d packages; want %d", got, want)
	}

}

func openRepositoryFromArchive(t *testing.T, ctx context.Context, testPath, name string) repository.Repository {
	t.Helper()

	tempdir := t.TempDir()
	tarfile := filepath.Join(testPath, fmt.Sprintf("%s-repository.tar", name))
	_, address := gitserver.ServeGitRepository(t, tarfile, tempdir)
	metadataStore := createMetadataStoreFromArchive(t, fmt.Sprintf("%s-metadata.yaml", name), name)

	apiRepo := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.TypeRepository.Kind,
			APIVersion: v1alpha1.TypeRepository.APIVersion(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1alpha1.RepositorySpec{
			Deployment: false,
			Type:       v1alpha1.RepositoryTypeGit,
			Git: &v1alpha1.GitRepository{
				Repo: address,
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := k8sfake.NewClientBuilder().WithScheme(scheme).WithObjects(apiRepo).Build()
	cache := &Cache{
		repositories:  repomap.SafeRepoMap{},
		metadataStore: metadataStore,
		options: cachetypes.CacheOptions{
			ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
				LocalDirectory:             t.TempDir(),
				UseUserDefinedCaBundle:     true,
				CredentialResolver:         &fakecache.CredentialResolver{},
				RepoOperationRetryAttempts: 3,
			},
			CoreClient:           fakeClient,
			RepoPRChangeNotifier: &fakecache.ObjectNotifier{},
		}}
	cachedRepo, err := cache.OpenRepository(ctx, apiRepo)
	if err != nil {
		t.Fatalf("OpenRepository(%q) of %q failed; %v", address, tarfile, err)
	}

	t.Cleanup(func() {
		err := cache.CloseRepository(ctx, apiRepo, []v1alpha1.Repository{*apiRepo})
		if err != nil {
			t.Errorf("CloseRepository(%q) failed: %v", address, err)
		}
		if len(cache.GetRepositories()) != 0 {
			t.Errorf("CloseRepository hasn't deleted repository from cache")
		}
	})
	return cachedRepo
}

func createMetadataStoreFromArchive(t *testing.T, testPath, name string) meta.MetadataStore {
	t.Helper()

	f := filepath.Join("..", "git", "testdata", testPath)
	c, err := os.ReadFile(f)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("Error reading metadata file found for repository %s", name)
	}
	if os.IsNotExist(err) {
		return &fakemeta.MemoryMetadataStore{
			Metas: []metav1.ObjectMeta{},
		}
	}

	var metas []metav1.ObjectMeta
	if err := yaml.Unmarshal(c, &metas); err != nil {
		t.Fatalf("Error unmarshalling metadata file for repository %s", name)
	}

	return &fakemeta.MemoryMetadataStore{
		Metas: metas,
	}
}

func TestFindUpstreamReference(t *testing.T) {
	ctx := context.Background()
	cache := &Cache{repositories: repomap.SafeRepoMap{}}
	repoKey := repository.RepositoryKey{Namespace: "test-ns", Name: "test-repo"}
	mockRepo := &cachedRepository{
		key:                    repoKey,
		cachedPackageRevisions: make(map[repository.PackageRevisionKey]*cachedPackageRevision),
	}
	_, _ = cache.repositories.LoadOrCreate(repoKey, func() (repository.Repository, error) {
		return mockRepo, nil
	})

	// Create second repository for cross-repo testing
	repo2Key := repository.RepositoryKey{Namespace: "test-ns", Name: "test-repo2"}
	mockRepo2 := &cachedRepository{
		key:                    repo2Key,
		cachedPackageRevisions: make(map[repository.PackageRevisionKey]*cachedPackageRevision),
	}
	_, _ = cache.repositories.LoadOrCreate(repo2Key, func() (repository.Repository, error) {
		return mockRepo2, nil
	})

	addDownstream := func(repo *cachedRepository, downstreamPkgName, taskType, upstreamRefName string) {
		key := repository.PackageRevisionKey{
			PkgKey:        repository.PackageKey{RepoKey: repo.key, Package: downstreamPkgName},
			WorkspaceName: "v1",
		}
		var task porchapi.Task
		switch taskType {
		case "clone":
			task = porchapi.Task{
				Type:  "clone",
				Clone: &porchapi.PackageCloneTaskSpec{Upstream: porchapi.UpstreamPackage{UpstreamRef: &porchapi.PackageRevisionRef{Name: upstreamRefName}}},
			}
		case "upgrade":
			task = porchapi.Task{
				Type:    "upgrade",
				Upgrade: &porchapi.PackageUpgradeTaskSpec{NewUpstream: porchapi.PackageRevisionRef{Name: upstreamRefName}},
			}
		}
		repo.cachedPackageRevisions[key] = &cachedPackageRevision{
			PackageRevision: &fake.FakePackageRevision{
				PrKey:           key,
				PackageRevision: &porchapi.PackageRevision{Spec: porchapi.PackageRevisionSpec{Tasks: []porchapi.Task{task}}},
			},
		}
	}

	tests := map[string]struct {
		namespace           string
		upstreamPkgToDelete string
		repo                *cachedRepository
		downstreamPkgName   string
		upstreamRefName     string
		taskType            string
		wantDep             string
	}{
		"no downstream": {
			namespace:           "test-ns",
			upstreamPkgToDelete: "test-repo.upstream.v1",
			wantDep:             "",
		},
		"find clone downstream": {
			namespace:           "test-ns",
			upstreamPkgToDelete: "test-repo.upstream.v1",
			repo:                mockRepo,
			downstreamPkgName:   "downstream",
			upstreamRefName:     "test-repo.upstream.v1",
			taskType:            "clone",
			wantDep:             "test-repo.downstream.v1",
		},
		"find upgrade downstream": {
			namespace:           "test-ns",
			upstreamPkgToDelete: "test-repo.upstream.v1",
			repo:                mockRepo,
			downstreamPkgName:   "upgrade",
			upstreamRefName:     "test-repo.upstream.v1",
			taskType:            "upgrade",
			wantDep:             "test-repo.upgrade.v1",
		},
		"task without downstream": {
			namespace:           "test-ns",
			upstreamPkgToDelete: "test-repo.upstream.v1",
			repo:                mockRepo,
			downstreamPkgName:   "other",
			upstreamRefName:     "test-repo.different.v1",
			taskType:            "clone",
			wantDep:             "",
		},
		"different namespace": {
			namespace:           "other-ns",
			upstreamPkgToDelete: "test-repo.upstream.v1",
			wantDep:             "",
		},
		"cross-repo downstream": {
			namespace:           "test-ns",
			upstreamPkgToDelete: "test-repo.base-pkg.v1",
			repo:                mockRepo2,
			downstreamPkgName:   "derived",
			upstreamRefName:     "test-repo.base-pkg.v1",
			taskType:            "clone",
			wantDep:             "test-repo2.derived.v1",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			mockRepo.cachedPackageRevisions = make(map[repository.PackageRevisionKey]*cachedPackageRevision)
			mockRepo2.cachedPackageRevisions = make(map[repository.PackageRevisionKey]*cachedPackageRevision)
			if tt.taskType != "" {
				addDownstream(tt.repo, tt.downstreamPkgName, tt.taskType, tt.upstreamRefName)
			}
			dep, err := cache.FindAllUpstreamReferencesInRepositories(ctx, tt.namespace, tt.upstreamPkgToDelete)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDep, dep)
		})
	}
}
