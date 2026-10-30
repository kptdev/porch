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
	"strings"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

var (
	res = &fakeReferenceResolver{}
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
