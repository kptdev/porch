// Copyright 2025 The kpt Authors
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

package task

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	res = &fakeReferenceResolver{
		address:   "https://github.com/example/repo.git",
		directory: "basens",
	}
)

func createMockedResources() repository.PackageResources {
	return repository.PackageResources{
		Contents: map[string]string{
			"apiVersion": "kpt.dev/v1alpha1",
			"kind":       "Kptfile",
		},
	}
}

func TestApplyErrorInvalidUpstreamUpgrade(t *testing.T) {
	ctx := context.Background()

	// Mock resources and tasks with valid upstream but no resources to fetch
	resources := createMockedResources()
	updateTask := &porchapi.Task{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: "original",
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: "upstream",
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: "destination",
			},
		},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: updateTask,
		pkgName:     "test-package",
	}

	_, _, err := mutation.apply(ctx, resources)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error fetching the resources for package")
}

func TestUpgradeToPlaceholderAsNewUpstream(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "1234567890"
	revision := -1
	workspace := "main"
	placeholderPackageName := "repo.1234567890.main"
	placeholderPackageRevision := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName:    pkg,
				Revision:       revision,
				RepositoryName: repositoryName,
				Resources: map[string]string{
					kptfilev1.KptFileName: strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
					`),
				},
			},
		},
	}

	oldRev := *placeholderPackageRevision
	oldPackageRevision := &oldRev
	oldPackageRevision.PrKey.WorkspaceName = "old"

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			oldPackageRevision,
			placeholderPackageRevision,
		},
	}
	repoOpener := &fakeRepositoryOpener{
		repository: repo,
	}

	mutation := upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream: porchapi.PackageRevisionRef{
					Name: "repo.1234567890.old",
				},
				NewUpstream: porchapi.PackageRevisionRef{
					Name: placeholderPackageName,
				},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{
					Name: "destination",
				},
			},
		},

		namespace:         "test-namespace",
		pkgName:           pkg,
		referenceResolver: res,
		repoOpener:        repoOpener,
	}
	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error upgrading to the placeholder package revision as new upstream")

	mutation.referenceResolver = (&errorAfterNReferenceResolver{r: res, err: errors.New("error resolving reference")}).startAt(2)

	_, _, err = mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "failed to resolve repository reference")

	mutation.referenceResolver = res
}

// TestUpgradeMismatchedUpstreamPackages verifies that the server rejects an
// upgrade task when the old and new upstream references do not point at the
// same package. This is the server-side guard for mismatched refs supplied
// in a raw API call.
func TestUpgradeMismatchedUpstreamPackages(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	localPkg := "nf01"
	oldUpstreamPkg := "upstream-other-function"
	newUpstreamPkg := "upstream-yetanother-function"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
`)

	gitKptfile := func(pkg, ref string) kptfilev1.KptFile {
		return kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: ref, Directory: "/" + pkg},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: ref, Directory: "/" + pkg, Commit: "abc123"},
			},
		}
	}

	// The old upstream the local package was actually cloned from.
	oldUpstreamPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
				Package: oldUpstreamPkg,
			},
			Revision:      1,
			WorkspaceName: "firstver",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
			},
		},
		Kptfile: gitKptfile(oldUpstreamPkg, "v1"),
	}

	// A new upstream that belongs to a DIFFERENT package.
	newUpstreamPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
				Package: newUpstreamPkg,
			},
			Revision:      2,
			WorkspaceName: "secondver",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
			},
		},
		Kptfile: gitKptfile(newUpstreamPkg, "v2"),
	}

	// The local (target) package revision, cloned from oldUpstreamPR.
	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
				Package: localPkg,
			},
			Revision:      1,
			WorkspaceName: "clone01",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    localPkg,
				RepositoryName: repositoryName,
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{
									Name: "repo.upstream-other-function.firstver",
								},
							},
						},
					},
				},
			},
		},
		Kptfile: gitKptfile(oldUpstreamPkg, "v1"),
	}

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			oldUpstreamPR,
			newUpstreamPR,
			localPR,
		},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream: porchapi.PackageRevisionRef{
					Name: "repo.upstream-other-function.firstver",
				},
				NewUpstream: porchapi.PackageRevisionRef{
					Name: "repo.upstream-yetanother-function.secondver",
				},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{
					Name: "repo.nf01.clone01",
				},
				Strategy: porchapi.ForceDeleteReplace,
			},
		},
		namespace:         repositoryNamespace,
		pkgName:           localPkg,
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "invalid upstream package reference",
		"Expected the upgrade to be rejected because old and new upstream refer to different packages")
}

// TestUpgradeUpstreamPackageDiffersFromLocalOrigin verifies that the server
// rejects an upgrade whose upstream refs agree with each other but do NOT match
// the package the local revision was originally derived from.
func TestUpgradeUpstreamPackageDiffersFromLocalOrigin(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	localPkg := "nf01"
	originPkg := "upstream-function"
	otherPkg := "upstream-other-function"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
`)

	gitKptfile := func(pkg, ref string) kptfilev1.KptFile {
		return kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: ref, Directory: "/" + pkg},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: ref, Directory: "/" + pkg, Commit: "abc123"},
			},
		}
	}

	mkPR := func(pkg, ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
					Package: pkg,
				},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
				},
			},
			Kptfile: gitKptfile(pkg, fmt.Sprintf("v%d", rev)),
		}
	}

	// old and new upstream agree on package "upstream-other-function"...
	oldUpstreamPR := mkPR(otherPkg, "firstver", 1)
	newUpstreamPR := mkPR(otherPkg, "secondver", 2)

	// ...but the local package was actually cloned from "upstream-function".
	localPR := mkPR(localPkg, "clone01", 1)
	localPR.PackageRevision = &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    localPkg,
			RepositoryName: repositoryName,
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						Upstream: porchapi.UpstreamPackage{
							UpstreamRef: &porchapi.PackageRevisionRef{
								Name: "repo.upstream-function.v1",
							},
						},
					},
				},
			},
		},
	}
	localPR.Kptfile = gitKptfile(originPkg, "v1")

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{oldUpstreamPR, newUpstreamPR, localPR},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.upstream-other-function.firstver"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.upstream-other-function.secondver"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.clone01"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         repositoryNamespace,
		pkgName:           localPkg,
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "invalid upstream package reference",
		"Expected rejection because upstream refs do not match the local package's origin")
}

// TestUpgradeEditOriginSourceDeletedUsesLock covers the case where the local
// package is an edited revision whose edit source has been deleted. History
// resolution hits a NotFound; validation must fall back to the surviving
// revision's Status.UpstreamLock to recover the upstream package.
func TestUpgradeEditOriginSourceDeletedUsesLock(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	upstreamPkg := "basens"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
					Package: upstreamPkg,
				},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
				},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git",
					Ref: "basens/v1", Directory: "basens"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git",
					Ref: "basens/v1", Directory: "basens", Commit: "abc"}},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{Revision: rev},
			},
		}
	}

	oldUpstreamPR := mkUpstream("v1", 1)
	newUpstreamPR := mkUpstream("v2", 2)

	// Local: an edited package whose edit source ("repo.deleted-source.v1") is
	// NOT present in the repo, but which retains a valid Status.UpstreamLock
	// pointing at the basens upstream.
	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
				Package: "nf01",
			},
			Revision:      1,
			WorkspaceName: "edit01",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    "nf01",
				RepositoryName: repositoryName,
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeEdit,
						Edit: &porchapi.PackageEditTaskSpec{
							Source: &porchapi.PackageRevisionRef{Name: "repo.deleted-source.v1"},
						},
					},
				},
			},
			Status: porchapi.PackageRevisionStatus{
				UpstreamLock: &porchapi.Locator{
					Type: porchapi.OriginType("git"),
					Git: &porchapi.GitLock{
						Repo:      "https://github.com/example/repo.git",
						Directory: "basens",
						Ref:       "basens/v1",
						Commit:    "abc",
					},
				},
			},
		},
	}

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{oldUpstreamPR, newUpstreamPR, localPR},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v1"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.edit01"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         repositoryNamespace,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.NoError(t, err, "edit-origin upgrade should recover upstream package from Status.UpstreamLock when the edit source is deleted")
}

// TestUpgradeEditOriginSourceDeletedNoLock covers the case where the edit
// source has been deleted AND the surviving revision has no Status.UpstreamLock
// to fall back on. Origin cannot be recovered, so validation must fail with the
// "upstream source not found" error (not a panic or a different error).
func TestUpgradeEditOriginSourceDeletedNoLock(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	upstreamPkg := "basens"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
					Package: upstreamPkg,
				},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
				},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git",
					Ref: "basens/v1", Directory: "basens"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git",
					Ref: "basens/v1", Directory: "basens", Commit: "abc"}},
			},
		}
	}

	oldUpstreamPR := mkUpstream("v1", 1)
	newUpstreamPR := mkUpstream("v2", 2)

	// Local: edited package whose edit source is absent AND which has no
	// Status.UpstreamLock, so the origin cannot be recovered at all.
	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
				Package: "nf01",
			},
			Revision:      1,
			WorkspaceName: "edit01",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    "nf01",
				RepositoryName: repositoryName,
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeEdit,
						Edit: &porchapi.PackageEditTaskSpec{
							Source: &porchapi.PackageRevisionRef{Name: "repo.deleted-source.v1"},
						},
					},
				},
			},
			// No Status.UpstreamLock.
		},
	}

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{oldUpstreamPR, newUpstreamPR, localPR},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v1"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.edit01"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         repositoryNamespace,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upstream source not found",
		"with no recoverable origin, validation must fail with the upstream-source-not-found error")
}

// TestUpgradeEditOriginLockPackageMismatch covers the case where the edit
// source is deleted and a Status.UpstreamLock exists, but the package it points
// at ("basens") differs from the package named by OldUpstream/NewUpstream
// ("istions"). The lock-based relaxation must still reject a cross-package
// upgrade.
func TestUpgradeEditOriginLockPackageMismatch(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
`)

	// Upstream PRs are for package "istions" (the refs in the request)...
	mkUpstream := func(pkg, ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
					Package: pkg,
				},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
				},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git",
					Ref: pkg + "/v1", Directory: pkg}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git",
					Ref: pkg + "/v1", Directory: pkg, Commit: "abc"}},
			},
		}
	}

	oldUpstreamPR := mkUpstream("istions", "v1", 1)
	newUpstreamPR := mkUpstream("istions", "v2", 2)

	// ...but the local edited package's surviving lock points at "basens".
	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
				Package: "nf01",
			},
			Revision:      1,
			WorkspaceName: "edit01",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    "nf01",
				RepositoryName: repositoryName,
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeEdit,
						Edit: &porchapi.PackageEditTaskSpec{
							Source: &porchapi.PackageRevisionRef{Name: "repo.deleted-source.v1"},
						},
					},
				},
			},
			Status: porchapi.PackageRevisionStatus{
				UpstreamLock: &porchapi.Locator{
					Type: porchapi.OriginType("git"),
					Git: &porchapi.GitLock{
						Repo:      "https://github.com/example/repo.git",
						Directory: "basens",
						Ref:       "basens/v1",
						Commit:    "abc",
					},
				},
			},
		},
	}

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{oldUpstreamPR, newUpstreamPR, localPR},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.istions.v1"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.istions.v2"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.edit01"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         repositoryNamespace,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "invalid upstream package reference",
		"lock-recovered origin (basens) must still reject upstream refs for a different package (istions)")
}

func TestUpgradeSubpackageDir(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "my-subpkg"
	workspace := "ws"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
upstream:
  git:
    repo: https://github.com/example/repo.git
    ref: my-subpkg/v1
    directory: my-subpkg
info:
  description: sample description
`)

	// Old upstream package revision
	oldUpstreamPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      1,
			WorkspaceName: "old",
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					kptfilev1.KptFileName: kptfileContent,
				},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{Revision: 1},
		},
	}

	// New upstream package revision
	newUpstreamPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      2,
			WorkspaceName: "new",
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					kptfilev1.KptFileName: kptfileContent,
					"new-resource.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: new-cm\n",
				},
			},
		},
		Kptfile: kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "v2", Directory: "/pkg"},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "v2", Directory: "/pkg", Commit: "def456"},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{Revision: 2},
		},
	}

	t.Run("Error when SubpackageDir does not exist in local resources", func(t *testing.T) {
		// Local package revision has no content at the specified SubpackageDir
		localPR := &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{
						Namespace: repositoryNamespace,
						Name:      repositoryName,
					},
					Package: pkg,
				},
				Revision:      1,
				WorkspaceName: workspace,
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName: kptfileContent,
						"root-resource.yaml":  "some content",
					},
				},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    pkg,
					RepositoryName: repositoryName,
					Tasks: []porchapi.Task{{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{Name: "repo.mypkg.old"},
							},
						},
					}},
				},
			},
		}

		repo := &fake.Repository{
			PackageRevisions: []repository.PackageRevision{
				oldUpstreamPR,
				newUpstreamPR,
				localPR,
			},
		}

		mutation := &upgradePackageMutation{
			upgradeTask: &porchapi.Task{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					OldUpstream: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.old",
					},
					NewUpstream: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.new",
					},
					LocalPackageRevisionRef: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.ws",
					},
					SubpackageDir: "nonexistent-subpkg",
				},
			},
			namespace:         repositoryNamespace,
			pkgName:           pkg,
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{repository: repo},
		}

		_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `subpackage "nonexistent-subpkg" not found in package`)
	})

	t.Run("Success when SubpackageDir exists in local resources", func(t *testing.T) {
		// Local package revision has content at the specified SubpackageDir
		localPR := &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{
						Namespace: repositoryNamespace,
						Name:      repositoryName,
					},
					Package: pkg,
				},
				Revision:      1,
				WorkspaceName: workspace,
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName:            kptfileContent,
						"root-resource.yaml":             "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: root-cm\n",
						"my-subpkg/Kptfile":              kptfileContent,
						"my-subpkg/subpkg-resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: subpkg-cm\n",
					},
				},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    pkg,
					RepositoryName: repositoryName,
					Tasks: []porchapi.Task{{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{Name: "repo.mypkg.old"},
							},
						},
					}},
				},
			},
		}

		repo := &fake.Repository{
			PackageRevisions: []repository.PackageRevision{
				oldUpstreamPR,
				newUpstreamPR,
				localPR,
			},
		}

		mutation := &upgradePackageMutation{
			upgradeTask: &porchapi.Task{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					OldUpstream: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.old",
					},
					NewUpstream: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.new",
					},
					LocalPackageRevisionRef: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.ws",
					},
					SubpackageDir: "my-subpkg",
				},
			},
			namespace:         repositoryNamespace,
			pkgName:           pkg,
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{repository: repo},
		}

		result, taskResult, err := mutation.apply(context.Background(), repository.PackageResources{})
		assert.NoError(t, err)
		assert.NotNil(t, taskResult)
		// The result should contain the merged resources based on the subpackage content only,
		// not the root-level resources or prefixed paths
		assert.Contains(t, result.Contents, kptfilev1.KptFileName)
		assert.Contains(t, result.Contents, "subpkg-resource.yaml")
		assert.NotEmpty(t, result.Contents["subpkg-resource.yaml"])
		assert.NotContains(t, result.Contents, "root-resource.yaml")
		assert.NotContains(t, result.Contents, "my-subpkg/subpkg-resource.yaml")
	})

	t.Run("Error when SubpackageDir has no trailing content match", func(t *testing.T) {
		// Local has a file that starts with the subpackage dir name but without the "/" separator
		// e.g. "my-subpkg-extra/file.yaml" should NOT match SubpackageDir "my-subpkg"
		localPR := &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{
						Namespace: repositoryNamespace,
						Name:      repositoryName,
					},
					Package: pkg,
				},
				Revision:      1,
				WorkspaceName: workspace,
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName:           kptfileContent,
						"my-subpkg-extra/resource.yaml": "content",
					},
				},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    pkg,
					RepositoryName: repositoryName,
					Tasks: []porchapi.Task{{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{Name: "repo.mypkg.old"},
							},
						},
					}},
				},
			},
		}

		repo := &fake.Repository{
			PackageRevisions: []repository.PackageRevision{
				oldUpstreamPR,
				newUpstreamPR,
				localPR,
			},
		}

		mutation := &upgradePackageMutation{
			upgradeTask: &porchapi.Task{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					OldUpstream: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.old",
					},
					NewUpstream: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.new",
					},
					LocalPackageRevisionRef: porchapi.PackageRevisionRef{
						Name: "repo.my-subpkg.ws",
					},
					SubpackageDir: "my-subpkg",
				},
			},
			namespace:         repositoryNamespace,
			pkgName:           pkg,
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{repository: repo},
		}

		_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `subpackage "my-subpkg" not found in package`)
	})

	// subpkgKptfile builds a subpackage Kptfile string whose managed upstream
	// points at the given directory (ref = dir/v1)
	subpkgKptfile := func(dir string) string {
		return strings.TrimSpace(fmt.Sprintf(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
upstream:
  git:
    repo: https://github.com/example/repo.git
    ref: %s/v1
    directory: %s
info:
  description: sample description
`, dir, dir))
	}

	// upstreamFake builds a published upstream PR fake with the given package
	// path/leaf, workspace and revision, carrying minimal resources + git lock
	// so it can be fetched during apply.
	upstreamFake := func(pkgPath, pkgLeaf, ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
					Path:    pkgPath,
					Package: pkgLeaf,
				},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: kptfileContent},
				},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git",
					Ref: "v" + fmt.Sprint(rev), Directory: "/" + pkgLeaf}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git",
					Ref: "v" + fmt.Sprint(rev), Directory: "/" + pkgLeaf, Commit: "abc123"}},
			},
			PackageRevision: &porchapi.PackageRevision{Spec: porchapi.PackageRevisionSpec{Revision: rev}},
		}
	}

	// localForSubpkg builds a local parent PR whose SubpackageDir contains a
	// Kptfile with the given upstream directory.
	localForSubpkg := func(subDir, upstreamDir string) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: repositoryNamespace, Name: repositoryName},
					Package: pkg,
				},
				Revision:      1,
				WorkspaceName: workspace,
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName:                     kptfileContent,
						path.Join(subDir, "Kptfile"):              subpkgKptfile(upstreamDir),
						path.Join(subDir, "subpkg-resource.yaml"): "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: subpkg-cm\n",
					},
				},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    pkg,
					RepositoryName: repositoryName,
					Tasks: []porchapi.Task{{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{Name: "repo.mypkg.old"},
							},
						},
					}},
				},
			},
		}
	}

	mutationFor := func(prs []repository.PackageRevision, oldUp, newUp, subDir string) *upgradePackageMutation {
		return &upgradePackageMutation{
			upgradeTask: &porchapi.Task{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					OldUpstream:             porchapi.PackageRevisionRef{Name: oldUp},
					NewUpstream:             porchapi.PackageRevisionRef{Name: newUp},
					LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.my-subpkg.ws"},
					SubpackageDir:           subDir,
				},
			},
			namespace:         repositoryNamespace,
			pkgName:           pkg,
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{repository: &fake.Repository{PackageRevisions: prs}},
		}
	}

	t.Run("Reject when old upstream package differs from subpackage upstream", func(t *testing.T) {
		// Subpackage's Kptfile upstream is "my-subpkg", but OldUpstream names a
		// different package ("other-subpkg"). Both refs are fetchable so that
		// the failure is unambiguously the package-identity validation.
		local := localForSubpkg("my-subpkg", "my-subpkg")
		otherOld := upstreamFake("", "other-subpkg", "old", 1)
		mySubNew := upstreamFake("", "my-subpkg", "new", 2)
		prs := []repository.PackageRevision{otherOld, mySubNew, local}

		m := mutationFor(prs, "repo.other-subpkg.old", "repo.my-subpkg.new", "my-subpkg")
		_, _, err := m.apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "invalid upstream package reference",
			"old upstream package must match the subpackage's actual upstream package")
	})

	t.Run("Reject when new upstream package differs from subpackage upstream", func(t *testing.T) {
		// old upstream matches the subpackage ("my-subpkg"), but NewUpstream
		// names a different package ("other-subpkg").
		local := localForSubpkg("my-subpkg", "my-subpkg")
		mySubOld := upstreamFake("", "my-subpkg", "old", 1)
		otherNew := upstreamFake("", "other-subpkg", "new", 2)
		prs := []repository.PackageRevision{mySubOld, otherNew, local}

		m := mutationFor(prs, "repo.my-subpkg.old", "repo.other-subpkg.new", "my-subpkg")
		_, _, err := m.apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "invalid upstream package reference",
			"new upstream package must be the same package as the subpackage's upstream")
	})

	t.Run("Accept nested subpackage upstream directory", func(t *testing.T) {
		// Subpackage upstream directory is nested ("functions/nf-base"); the
		// upstream refs decompose to Path="functions", Package="nf-base", which
		// must compare equal to the Kptfile-derived "functions/nf-base".
		local := localForSubpkg("my-subpkg", "functions/nf-base")
		nestedOld := upstreamFake("functions", "nf-base", "old", 1)
		nestedNew := upstreamFake("functions", "nf-base", "new", 2)
		prs := []repository.PackageRevision{nestedOld, nestedNew, local}

		m := mutationFor(prs, "repo.functions.nf-base.old", "repo.functions.nf-base.new", "my-subpkg")
		result, taskResult, err := m.apply(context.Background(), repository.PackageResources{})
		assert.NoError(t, err, "nested subpackage upstream path should validate")
		assert.NotNil(t, taskResult)
		assert.Contains(t, result.Contents, kptfilev1.KptFileName)
	})
}

// TestUpgradeSubpackageUpstreamRepoAware exercises repository- and
// revision-aware subpackage validation via apply(): OldUpstream must
// match the subpackage's recorded upstream repo+package+revision (the
// merge base), and NewUpstream must be the same repo+package. Guards
// cross-repo and wrong-merge-base upgrades.
func TestUpgradeSubpackageUpstreamRepoAware(t *testing.T) {
	ns := "test-namespace"
	const (
		urlA = "https://github.com/example/repo-a.git"
		urlB = "https://github.com/example/repo-b.git"
	)

	parentKptfile := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: parent
`)

	// Subpackage's managed upstream: repo urlA, repo dir "stage", package "base",
	// revision v2  =>  Ref = stage/base/v2, Directory = base.
	subKptfile := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: base
upstream:
  type: git
  git:
    repo: https://github.com/example/repo-a.git
    ref: stage/base/v2
    directory: base
`)

	upstreamKptfile := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: base
`)

	// Upstream "base" package revision in the named repo, fully fetchable
	// (resources + lock) so apply() can fetch and, on the happy path, merge.
	mkUpstream := func(repoName, ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName},
					Path:    "stage",
					Package: "base",
				},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: upstreamKptfile},
				},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{Revision: rev},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: urlA, Ref: "stage/base/" + ws, Directory: "base"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: urlA, Ref: "stage/base/" + ws,
					Directory: "base", Commit: "abc"}},
			},
		}
	}

	mkLocal := func() *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey: key.PackageKey{
					RepoKey: key.RepositoryKey{Namespace: ns, Name: "repo-a"},
					Package: "parent",
				},
				Revision:      1,
				WorkspaceName: "ws",
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName:       parentKptfile,
						"base-subpkg/Kptfile":       subKptfile,
						"base-subpkg/resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n",
					},
				},
			},
		}
	}

	resolver := &mapReferenceResolver{repos: map[string]*configapi.GitRepository{
		"repo-a": {Repo: urlA, Directory: "stage", Branch: "main"},
		"repo-b": {Repo: urlB, Directory: "stage", Branch: "main"},
	}}

	newMutation := func(prs []repository.PackageRevision, oldName, newName string) *upgradePackageMutation {
		return &upgradePackageMutation{
			upgradeTask: &porchapi.Task{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					OldUpstream:             porchapi.PackageRevisionRef{Name: oldName},
					NewUpstream:             porchapi.PackageRevisionRef{Name: newName},
					LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo-a.parent.ws"},
					SubpackageDir:           "base-subpkg",
					Strategy:                porchapi.ResourceMerge,
				},
			},
			namespace:         ns,
			pkgName:           "parent",
			referenceResolver: resolver,
			repoOpener:        &fakeRepositoryOpener{repository: &fake.Repository{PackageRevisions: prs}},
		}
	}

	t.Run("accept same repo-package combination", func(t *testing.T) {
		prs := []repository.PackageRevision{
			mkUpstream("repo-a", "v2", 2), // origin = stage/base/v2
			mkUpstream("repo-a", "v3", 3),
			mkLocal(),
		}
		m := newMutation(prs, "repo-a.stage.base.v2", "repo-a.stage.base.v3")
		_, _, err := m.apply(context.Background(), repository.PackageResources{})
		assert.NoError(t, err)
	})

	t.Run("reject cross-repo old upstream", func(t *testing.T) {
		prs := []repository.PackageRevision{
			mkUpstream("repo-b", "v2", 2), // repo-b (urlB) != origin repo-a (urlA)
			mkUpstream("repo-a", "v3", 3),
			mkLocal(),
		}
		m := newMutation(prs, "repo-b.stage.base.v2", "repo-a.stage.base.v3")
		_, _, err := m.apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "invalid upstream package reference")
		assert.ErrorContains(t, err, "repository")
	})

	t.Run("reject wrong merge-base revision", func(t *testing.T) {
		prs := []repository.PackageRevision{
			mkUpstream("repo-a", "v1", 1), // old rev v1 != origin ref v2
			mkUpstream("repo-a", "v3", 3),
			mkLocal(),
		}
		m := newMutation(prs, "repo-a.stage.base.v1", "repo-a.stage.base.v3")
		_, _, err := m.apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "invalid upstream package reference")
		assert.ErrorContains(t, err, "revision")
	})

	t.Run("reject cross-repo new upstream", func(t *testing.T) {
		prs := []repository.PackageRevision{
			mkUpstream("repo-a", "v2", 2),
			mkUpstream("repo-b", "v3", 3), // new in repo-b != origin repo-a
			mkLocal(),
		}
		m := newMutation(prs, "repo-a.stage.base.v2", "repo-b.stage.base.v3")
		_, _, err := m.apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "invalid upstream package reference")
	})
}

// TestUpgradeWholePackageUpgradeOrigin covers findUpstreamName's TaskTypeUpgrade
// arm: a local package whose recorded origin is itself an upgrade task resolves
// its upstream from that task's NewUpstream.
func TestUpgradeWholePackageUpgradeOrigin(t *testing.T) {
	ns := "test-namespace"
	repoName := "repo"
	upstreamPkg := "basens"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: upstreamPkg},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "basens/v1",
					Directory: "basens"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "basens/v1", Directory: "basens", Commit: "abc"}},
			},
		}
	}

	oldUpstreamPR := mkUpstream("v2", 2)
	newUpstreamPR := mkUpstream("v3", 3)

	// Local package's first task is an UPGRADE (not clone): its recorded origin
	// is the package named by that task's NewUpstream (repo.basens.v2).
	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "nf01"},
			Revision:      1,
			WorkspaceName: "ws",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    "nf01",
				RepositoryName: repoName,
				Tasks: []porchapi.Task{{
					Type: porchapi.TaskTypeUpgrade,
					Upgrade: &porchapi.PackageUpgradeTaskSpec{
						NewUpstream: porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
					},
				}},
			},
		},
	}

	repo := &fake.Repository{PackageRevisions: []repository.PackageRevision{oldUpstreamPR, newUpstreamPR, localPR}}
	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v3"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.ws"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         ns,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.NoError(t, err, "local origin via an upgrade task should resolve to NewUpstream's package")
}

// TestUpgradeWholePackageNoTasks covers findUpstreamName's no-tasks guard: a
// local package with an empty task list cannot have its origin determined.
func TestUpgradeWholePackageNoTasks(t *testing.T) {
	ns := "test-namespace"
	repoName := "repo"
	upstreamPkg := "basens"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: upstreamPkg},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "basens/v1",
					Directory: "basens"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "basens/v1", Directory: "basens", Commit: "abc"}},
			},
		}
	}

	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "nf01"},
			Revision:      1,
			WorkspaceName: "ws",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{PackageName: "nf01", RepositoryName: repoName, Tasks: []porchapi.Task{}},
		},
	}

	repo := &fake.Repository{PackageRevisions: []repository.PackageRevision{mkUpstream("v1", 1), mkUpstream("v2", 2), localPR}}
	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v1"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.ws"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         ns,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "has no tasks")
}

// TestUpgradeSubpackageOriginErrors covers resolveSubpackageOrigin's error arms:
// missing upstream, unmanaged upstream, and an invalid ref (not in format vN) .
func TestUpgradeSubpackageOriginErrors(t *testing.T) {
	ns := "test-namespace"
	repoName := "repo"

	parentKptfile := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: parent
upstream:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: base/notaversion
    directory: base
upstreamLock:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: base/notaversion
    directory: base
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "base"},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: parentKptfile}},
			},
			PackageRevision: &porchapi.PackageRevision{Spec: porchapi.PackageRevisionSpec{Revision: rev}},
		}
	}

	mkLocal := func(subKptfile string) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "parent"},
				Revision:      1,
				WorkspaceName: "ws",
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{
					kptfilev1.KptFileName: parentKptfile,
					"base/Kptfile":        subKptfile,
					"base/resource.yaml":  "x: y\n",
				}},
			},
		}
	}

	newMutation := func(local *fake.FakePackageRevision) *upgradePackageMutation {
		repo := &fake.Repository{PackageRevisions: []repository.PackageRevision{mkUpstream("v1", 1), mkUpstream("v2", 2),
			local}}
		return &upgradePackageMutation{
			upgradeTask: &porchapi.Task{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.base.v1"},
					NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.base.v2"},
					LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.parent.ws"},
					SubpackageDir:           "base",
					Strategy:                porchapi.ResourceMerge,
				},
			},
			namespace:         ns,
			pkgName:           "parent",
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{repository: repo},
		}
	}

	t.Run("no upstream", func(t *testing.T) {
		sub := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: base
`)
		_, _, err := newMutation(mkLocal(sub)).apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "has no upstream source")
	})

	t.Run("unmanaged upstream", func(t *testing.T) {
		// A non-managed ref (not in format "dir/vN") makes GetRepoPackageRefFromUpstream report isManaged=false.
		sub := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: base
upstream:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: main
    directory: base
`)
		_, _, err := newMutation(mkLocal(sub)).apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "not managed by kpt")
	})

	t.Run("invalid ref revision", func(t *testing.T) {
		// Managed form (dir/<ref>) but the ref leaf is not vN, so Revision2Int < 1.
		sub := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: base
upstream:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: base/notaversion
    directory: base
upstreamLock:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: base/notaversion
    directory: base
`)

		_, _, err := newMutation(mkLocal(sub)).apply(context.Background(), repository.PackageResources{})
		assert.ErrorContains(t, err, "invalid git ref")
	})
}

// TestUpgradeSubpackageNestedDirectory exercises resolvePackageOriginFromRef
// vs resolvePackageOriginFromKptfile when the upstream package lives under a
// non-trivial directory (a nested path and a repo base directory). The two
// origins are derived from different coordinate systems (git upstream directory
// vs. the PR key's package pathname); this asserts they still agree for a
// legitimately-matching upgrade, so a false reject would surface here.
func TestUpgradeSubpackageNestedDirectory(t *testing.T) {
	ns := "test-namespace"
	const repoName = "repo"
	const repoURL = "https://github.com/example/repo.git"

	// The upstream package is nested at stage/base within the repo.
	parentKptfile := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: parent
`)
	// Subpackage Kptfile points its managed upstream at stage/base/v2.
	subKptfile := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: base
upstream:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: stage/base/v2
    directory: stage/base
upstreamLock:
  type: git
  git:
    repo: https://github.com/example/repo.git
    ref: stage/base/v2
    directory: stage/base
    commit: abc123
`)

	// Upstream PRs for OldUpstream (stage/base@v2) and NewUpstream (stage/base@v3).
	// Note PrKey encodes Path:"stage", Package:"base" so ToPkgPathname() == "stage/base".
	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Path: "stage", Package: "base"},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: parentKptfile}},
			},
			PackageRevision: &porchapi.PackageRevision{Spec: porchapi.PackageRevisionSpec{Revision: rev}},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{
					Type: kptfilev1.GitOrigin,
					Git:  &kptfilev1.Git{Repo: repoURL, Ref: "stage/base/v" + strconv.Itoa(rev), Directory: "stage/base"},
				},
				UpstreamLock: &kptfilev1.Locator{
					Type: kptfilev1.GitOrigin,
					Git:  &kptfilev1.GitLock{Repo: repoURL, Ref: "stage/base/v" + strconv.Itoa(rev), Directory: "stage/base", Commit: "abc" + strconv.Itoa(rev)},
				},
			},
		}
	}

	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "parent"},
			Revision:      1,
			WorkspaceName: "ws",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{
				kptfilev1.KptFileName: parentKptfile,
				"base/Kptfile":        subKptfile,
				"base/resource.yaml":  "x: y\n",
			}},
		},
	}

	// Resolver maps repoName -> a GitRepository. Crucially, set Directory to the
	// repo base so resolvePackageOriginFromRef's repoURL normalization is exercised;
	// the package-path divergence (if any) comes from ToPkgPathname vs path.Join.
	resolver := &mapReferenceResolver{repos: map[string]*configapi.GitRepository{
		repoName: {Repo: repoURL, Directory: ""},
	}}

	repo := &fake.Repository{PackageRevisions: []repository.PackageRevision{mkUpstream("v2", 2), mkUpstream("v3", 3), localPR}}
	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.stage.base.v2"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.stage.base.v3"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.parent.ws"},
				SubpackageDir:           "base",
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         ns,
		pkgName:           "parent",
		referenceResolver: resolver,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.NoError(t, err, "a legitimately-matching upgrade with a nested upstream directory should be accepted; "+
		"a failure here means resolvePackageOriginFromRef (ToPkgPathname) and resolvePackageOriginFromKptfile (path.Join of git dir) disagree")
}

// TestUpgradeEditOriginNilSource covers findEditOrigin's guard for an edit task
// that names no source package revision.
func TestUpgradeEditOriginNilSource(t *testing.T) {
	ns := "test-namespace"
	repoName := "repo"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "basens"},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream:     &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "basens/v1", Directory: "basens"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "basens/v1", Directory: "basens", Commit: "abc"}},
			},
		}
	}

	// Local's first task is an edit with a nil Edit spec.
	localPR := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "nf01"},
			Revision:      1,
			WorkspaceName: "ws",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    "nf01",
				RepositoryName: repoName,
				Tasks: []porchapi.Task{{
					Type: porchapi.TaskTypeEdit,
					Edit: nil, // no source
				}},
			},
		},
	}

	repo := &fake.Repository{PackageRevisions: []repository.PackageRevision{mkUpstream("v1", 1), mkUpstream("v2", 2), localPR}}
	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v1"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.ws"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         ns,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "does not name a source package revision")
}

// TestUpgradeEditOriginMultiHop covers findEditOrigin's loop across a chain of
// edit tasks that ultimately resolves to a clone origin.
func TestUpgradeEditOriginMultiHop(t *testing.T) {
	ns := "test-namespace"
	repoName := "repo"

	kptfileContent := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
`)

	mkUpstream := func(ws string, rev int) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "basens"},
				Revision:      rev,
				WorkspaceName: ws,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream:     &kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "basens/v1", Directory: "basens"}},
				UpstreamLock: &kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "basens/v1", Directory: "basens", Commit: "abc"}},
			},
		}
	}

	// edit2 -> edit1 -> clone(from basens). All three are the local package's
	// own history; the chain must resolve to repo.basens.v2 (the clone origin).
	withTasks := func(name string, tasks []porchapi.Task) *fake.FakePackageRevision {
		return &fake.FakePackageRevision{
			PrKey: key.PackageRevisionKey{
				PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: ns, Name: repoName}, Package: "nf01"},
				Revision:      1,
				WorkspaceName: name,
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{kptfilev1.KptFileName: kptfileContent}},
			},
			PackageRevision: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{PackageName: "nf01", RepositoryName: repoName, Tasks: tasks},
			},
		}
	}

	editSource1 := withTasks("edit1", []porchapi.Task{{
		Type: porchapi.TaskTypeClone,
		Clone: &porchapi.PackageCloneTaskSpec{
			Upstream: porchapi.UpstreamPackage{UpstreamRef: &porchapi.PackageRevisionRef{Name: "repo.basens.v2"}},
		},
	}})
	// local (edit2) points at edit1 as its edit source.
	localPR := withTasks("ws", []porchapi.Task{{
		Type: porchapi.TaskTypeEdit,
		Edit: &porchapi.PackageEditTaskSpec{Source: &porchapi.PackageRevisionRef{Name: "repo.nf01.edit1"}},
	}})

	repo := &fake.Repository{PackageRevisions: []repository.PackageRevision{
		mkUpstream("v2", 2), mkUpstream("v3", 3), editSource1, localPR,
	}}
	mutation := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v2"},
				NewUpstream:             porchapi.PackageRevisionRef{Name: "repo.basens.v3"},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.nf01.ws"},
				Strategy:                porchapi.ResourceMerge,
			},
		},
		namespace:         ns,
		pkgName:           "nf01",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{repository: repo},
	}

	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.NoError(t, err, "multi-hop edit chain should resolve to the clone origin")
}

// erroringReferenceResolver always returns the configured error, to exercise the
// ResolveReference failure branch of upstreamRefIdentity.
type erroringReferenceResolver struct{ err error }

func (f *erroringReferenceResolver) ResolveReference(_ context.Context, _, _ string, _ repository.Object) error {
	return f.err
}

// TestUpstreamRefIdentityErrors exercises upstreamRefIdentity's error branches
// directly. These cannot be reached via apply(): the pre-validation fetches in
// apply() resolve/fetch the same refs first and would fail earlier, so the
// helper is called directly here.
func TestUpstreamRefIdentityErrors(t *testing.T) {
	ns := "test-namespace"
	ctx := context.Background()

	t.Run("malformed ref name", func(t *testing.T) {
		// A name PkgRevK8sName2Key cannot parse into a key.
		m := &upgradePackageMutation{
			upgradeTask:       &porchapi.Task{Upgrade: &porchapi.PackageUpgradeTaskSpec{}},
			namespace:         ns,
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{},
		}
		_, err := m.resolvePackageOriginFromRef(ctx, porchapi.PackageRevisionRef{Name: "not-a-valid-pkgrev-name"})
		assert.ErrorContains(t, err, "error resolving upstream package revision name")
	})

	t.Run("repository resolve failure", func(t *testing.T) {
		m := &upgradePackageMutation{
			upgradeTask:       &porchapi.Task{Upgrade: &porchapi.PackageUpgradeTaskSpec{}},
			namespace:         ns,
			referenceResolver: &erroringReferenceResolver{err: errors.New("boom")},
			repoOpener:        &fakeRepositoryOpener{},
		}
		_, err := m.resolvePackageOriginFromRef(ctx, porchapi.PackageRevisionRef{Name: "repo.base.v1"})
		assert.ErrorContains(t, err, "cannot resolve repository")
	})

	t.Run("repository is not git", func(t *testing.T) {
		// Resolver returns a Repository with nil Spec.Git.
		resolver := &mapReferenceResolver{repos: map[string]*configapi.GitRepository{
			"repo": nil,
		}}
		m := &upgradePackageMutation{
			upgradeTask:       &porchapi.Task{Upgrade: &porchapi.PackageUpgradeTaskSpec{}},
			namespace:         ns,
			referenceResolver: resolver,
			repoOpener:        &fakeRepositoryOpener{},
		}
		_, err := m.resolvePackageOriginFromRef(ctx, porchapi.PackageRevisionRef{Name: "repo.base.v1"})
		assert.ErrorContains(t, err, "is not a git repository")
	})

	t.Run("package revision fetch failure", func(t *testing.T) {
		// Repo resolves with a git spec, but the repo contains no matching
		// revision, so findPackageRevision -> FetchRevision returns NotFound.
		resolver := &mapReferenceResolver{repos: map[string]*configapi.GitRepository{
			"repo": {Repo: "https://github.com/example/repo.git", Directory: ""},
		}}
		m := &upgradePackageMutation{
			upgradeTask:       &porchapi.Task{Upgrade: &porchapi.PackageUpgradeTaskSpec{}},
			namespace:         ns,
			referenceResolver: resolver,
			repoOpener:        &fakeRepositoryOpener{repository: &fake.Repository{}}, // empty: no revisions
		}
		_, err := m.resolvePackageOriginFromRef(ctx, porchapi.PackageRevisionRef{Name: "repo.base.v1"})
		assert.ErrorContains(t, err, "cannot fetch upstream ref")
	})
}

func TestUpgradePlaceholderAsLocal(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "1234567890"
	revision := -1
	workspace := "main"
	placeholderPackageName := "repo.1234567890.main"
	placeholderPackageRevision := &fake.FakePackageRevision{
		PrKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName:    pkg,
				Revision:       revision,
				RepositoryName: repositoryName,
				Resources: map[string]string{
					kptfilev1.KptFileName: strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
					`),
				},
			},
		},
		PackageRevision: &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    pkg,
				RepositoryName: repositoryName,
				Tasks: []porchapi.Task{{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						Upstream: porchapi.UpstreamPackage{
							UpstreamRef: &porchapi.PackageRevisionRef{Name: "repo.1234567890.old"},
						},
					},
				}},
			},
		},
	}

	oldRev := *placeholderPackageRevision
	oldPackageRevision := &oldRev
	oldPackageRevision.PrKey.WorkspaceName = "old"

	newRev := *placeholderPackageRevision
	newPackageRevision := &newRev
	newPackageRevision.PrKey.WorkspaceName = "new"

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			oldPackageRevision,
			newPackageRevision,
			placeholderPackageRevision,
		},
	}
	repoOpener := &fakeRepositoryOpener{
		repository: repo,
	}

	mutation := upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream: porchapi.PackageRevisionRef{
					Name: "repo.1234567890.old",
				},
				NewUpstream: porchapi.PackageRevisionRef{
					Name: "repo.1234567890.new",
				},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{
					Name: placeholderPackageName,
				},
			},
		},

		namespace:         "test-namespace",
		pkgName:           pkg,
		referenceResolver: res,
		repoOpener:        repoOpener,
	}
	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error upgrading the placeholder package revision")

	mutation.referenceResolver = (&errorAfterNReferenceResolver{r: res, err: errors.New("error resolving reference")}).startAt(4)

	_, _, err = mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "failed to resolve repository reference")

	mutation.referenceResolver = res
}

// TestFindPackageUpstreamErrorArms covers findPackageUpstream's clone-with-nil-
// upstream error and the default (unrecognized task type) arm. Called directly:
// these are guards on the local revision's recorded first task.
func TestFindPackageUpstreamErrorArms(t *testing.T) {
	ctx := context.Background()
	m := &upgradePackageMutation{
		upgradeTask:       &porchapi.Task{Upgrade: &porchapi.PackageUpgradeTaskSpec{}},
		namespace:         "test-namespace",
		referenceResolver: res,
		repoOpener:        &fakeRepositoryOpener{},
	}

	t.Run("clone task with nil upstream ref", func(t *testing.T) {
		pr := &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v1"},
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{{Type: porchapi.TaskTypeClone, Clone: nil}},
			},
		}
		_, _, err := m.findPackageUpstream(ctx, pr)
		assert.ErrorContains(t, err, "does not name an upstream package revision")
	})

	t.Run("unrecognized task type returns empty origin", func(t *testing.T) {
		// The default arm returns ("", nil, nil): no name, no origin, no error.
		pr := &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v1"},
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{{Type: porchapi.TaskType("bogus")}},
			},
		}
		name, origin, err := m.findPackageUpstream(ctx, pr)
		assert.NoError(t, err)
		assert.Empty(t, name)
		assert.Nil(t, origin)
	})
}

// TestResolvePackageOriginFromLocatorNil covers the nil / nil-Git guard in
// resolvePackageOriginFromLocator.
func TestResolvePackageOriginFromLocatorNil(t *testing.T) {
	m := &upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{Name: "repo.pkg.ws"},
			},
		},
		namespace: "test-namespace",
	}

	t.Run("nil locator", func(t *testing.T) {
		_, err := m.resolvePackageOriginFromLocator(nil)
		assert.ErrorContains(t, err, "has no upstream source")
	})

	t.Run("locator with nil git", func(t *testing.T) {
		_, err := m.resolvePackageOriginFromLocator(&porchapi.Locator{Git: nil})
		assert.ErrorContains(t, err, "has no upstream source")
	})
}

// nilRepositoryPackageRevision is a PackageRevision whose GetPackageRevision returns
// (nil, nil), to exercise resolveLocalUpstreamKey's nil-metadata guard.
type nilRepositoryPackageRevision struct {
	repository.PackageRevision
	name string
}

func (f *nilRepositoryPackageRevision) GetPackageRevision(context.Context) (*porchapi.PackageRevision, error) {
	return nil, nil
}
func (f *nilRepositoryPackageRevision) KubeObjectName() string { return f.name }

// TestResolveLocalUpstreamKeyErrorArms covers the nil-metadata guard and the
// unparseable-upstream-name path of resolveLocalUpstreamKey.
func TestResolveLocalUpstreamKeyErrorArms(t *testing.T) {
	ctx := context.Background()
	ns := "test-namespace"

	t.Run("nil local metadata", func(t *testing.T) {
		m := &upgradePackageMutation{
			upgradeTask:       &porchapi.Task{Upgrade: &porchapi.PackageUpgradeTaskSpec{}},
			namespace:         ns,
			referenceResolver: res,
			repoOpener:        &fakeRepositoryOpener{},
		}
		local := &nilRepositoryPackageRevision{name: "repo.local.ws"}
		_, _, err := m.resolveLocalUpstreamKey(ctx, local)
		assert.ErrorContains(t, err, "error getting metadata for revision")
	})
}

// mapReferenceResolver resolves each repository name to a distinct
// GitRepository, so repository-aware subpackage validation can be exercised
// (the shared fakeReferenceResolver returns an identical repo for every name).
type mapReferenceResolver struct {
	repos map[string]*configapi.GitRepository // keyed by Porch repo name
}

func (f *mapReferenceResolver) ResolveReference(_ context.Context, namespace, name string, result repository.Object) error {
	repo, ok := result.(*configapi.Repository)
	if !ok {
		return nil
	}
	git, found := f.repos[name]
	if !found {
		return apierrors.NewNotFound(configapi.TypeRepository.GroupResource(), name)
	}
	*repo = configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git:  git,
		},
	}
	return nil
}
