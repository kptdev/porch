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

package api

import (
	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	istionsPackage = "istions"
	basensPackage  = "basens"
	basensDir      = "basens"
	basensRef      = "basens/v1"
	testWorkspace  = "test-workspace"
)

func (t *PorchSuite) TestCloneFromUpstream() {
	testBlueprintsRepo := t.GetTestBlueprintsRepoURL()
	// Register Upstream Repository
	t.RegisterGitRepositoryF(testBlueprintsRepo, suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var list porchapi.PackageRevisionList
	t.ListE(&list, client.InNamespace(t.Namespace))

	placeholderUpstreamPr := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Name: suiteutils.TestBlueprintsRepoName,
			},
			Package: basensPackage,
		},
		WorkspaceName: "main",
		Revision:      -1})

	realUpstreamPr := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Name: suiteutils.TestBlueprintsRepoName,
			},
			Package: basensPackage},
		Revision: 1})

	// Register the repository as 'downstream'
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), suiteutils.PorchTestRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Attempt to create PackageRevision from placeholder package revision in upstream repo
	clonedPr := t.CreatePackageSkeleton(suiteutils.PorchTestRepoName, istionsPackage, testWorkspace)
	clonedPr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{
						Name: placeholderUpstreamPr.Name,
					},
				},
			},
		},
	}

	err := t.Client.Create(t.GetContext(), clonedPr)
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error cloning from the placeholder package revision")

	// Create PackageRevision properly from upstream repo
	clonedPr.Spec.Tasks[0].Clone.Upstream.UpstreamRef.Name = realUpstreamPr.Name

	t.CreateF(clonedPr)

	// Get istions resources
	var istions porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      clonedPr.Name,
	}, &istions)

	kptfile := t.ParseKptfileF(&istions)
	t.validateKptfileBasics(kptfile, istionsPackage)
	t.validateUpstreamLock(kptfile, testBlueprintsRepo)
	t.validateUpstream(kptfile, testBlueprintsRepo)
	t.validatePackageResourcesSize(clonedPr)
}

func (t *PorchSuite) TestCloneIntoDeploymentRepository() {
	testBlueprintsRepo := t.GetTestBlueprintsRepoURL()

	// Register the deployment repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), suiteutils.PorchTestRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword, suiteutils.RepositoryOptions{RepOpts: suiteutils.WithDeployment()})

	// Register the upstream repository
	t.RegisterGitRepositoryF(testBlueprintsRepo, suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var upstreamPackages porchapi.PackageRevisionList
	t.ListE(&upstreamPackages, client.InNamespace(t.Namespace))
	upstreamPackage := t.MustFindPackageRevision(&upstreamPackages, repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Name: suiteutils.TestBlueprintsRepoName,
			},
			Package: basensPackage,
		},
		Revision:      1,
		WorkspaceName: "v1",
	})

	// Create PackageRevision from upstream repo
	pr := t.CreatePackageSkeleton(suiteutils.PorchTestRepoName, "istions-deployment", "clone-deployment-workspace")
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{
						Name: upstreamPackage.Name, // Package to be cloned
					},
				},
			},
		},
	}

	t.CreateF(pr)

	// Get istions resources
	var istions porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &istions)

	kptfile := t.ParseKptfileF(&istions)
	t.validateKptfileBasics(kptfile, "istions-deployment")
	t.validateUpstreamLock(kptfile, testBlueprintsRepo)
	t.validateUpstream(kptfile, testBlueprintsRepo)
	t.validatePackageResourcesSize(pr)

	// Check generated context
	var configmap corev1.ConfigMap
	t.FindAndDecodeF(&istions, "package-context.yaml", &configmap)
	if got, want := configmap.Name, "kptfile.kpt.dev"; got != want {
		t.Errorf("package context name: got %s, want %s", got, want)
	}
	if got, want := configmap.Data["name"], "istions-deployment"; got != want {
		t.Errorf("package context 'data.name': got %s, want %s", got, want)
	}
}

func (t *PorchSuite) TestCloneLeadingSlash() {
	const (
		repository = "clone-ls"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Clone the package. Use leading slash in the directory (regression test)
	new := t.CreatePackageCloneF(repository, "test-clone-ls", defaultWorkspace, basensRef, "/basens")

	var pr porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: new.Name}, &pr)
}

func (t *PorchSuite) TestPackageUpgrade() {
	const (
		gitRepository = "package-upgrade"
	)

	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Register the repository as 'downstream'
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), gitRepository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var list porchapi.PackageRevisionList
	t.ListE(&list, client.InNamespace(t.Namespace))

	basensV1 := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: suiteutils.TestBlueprintsRepoName}, Package: basensPackage}, Revision: 1})
	basensV2 := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: suiteutils.TestBlueprintsRepoName}, Package: basensPackage}, Revision: 2})

	// Create PackageRevision from upstream repo
	pr := t.CreatePackageSkeleton(gitRepository, "testns", testWorkspace)
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{
						Name: basensV1.Name,
					},
				},
			},
		},
	}
	t.CreateF(pr)

	var revisionResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &revisionResources)

	revisionResources.Spec.Resources["config-map.yaml"] = `apiVersion: v1
kind: ConfigMap
metadata:
  name: update-resources-configmap
  namespace: example
data:
  value: Update Resources and Render
`
	t.UpdateF(&revisionResources)

	// publish PackageRevision
	t.GetF(client.ObjectKeyFromObject(pr), pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	published := t.UpdateApprovalF(pr)

	// Create new PackageRevision for upgrade workspace
	upgradePr := t.CreatePackageSkeleton(gitRepository, "testns", testWorkspace+"-upgrade")
	upgradePr.Spec.Tasks = []porchapi.Task{{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: basensV1.Name,
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: basensV2.Name,
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: published.Name,
			},
		},
	}}
	t.CreateF(upgradePr)

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      upgradePr.Name,
	}, &revisionResources)

	if _, found := revisionResources.Spec.Resources["resourcequota.yaml"]; !found {
		t.Errorf("Updated package should contain 'resourcequota.yaml` file")
	}

	// publish upgraded PackageRevision
	t.GetF(client.ObjectKeyFromObject(upgradePr), upgradePr)
	upgradePr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(upgradePr)
	upgradePr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	upgradePr = t.UpdateApprovalF(upgradePr)

	// Check its package size
	t.validatePackageResourcesSize(upgradePr)

	basensMain := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: suiteutils.TestBlueprintsRepoName}, Package: basensPackage}, Revision: -1})
	upgradePrTwo := t.CreatePackageSkeleton(gitRepository, "testns", testWorkspace+"-main-upgrade")

	upgradePrTwo.Spec.Tasks = []porchapi.Task{{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: basensV2.Name,
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: basensMain.Name,
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: upgradePr.Name,
			},
		},
	}}

	err := t.Client.Create(t.GetContext(), upgradePrTwo)
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error upgrading to the placeholder package revision")

	upgradePrTwo.Spec.Tasks = []porchapi.Task{{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: basensV1.Name,
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: basensV2.Name,
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: basensMain.Name,
			},
		},
	}}

	err = t.Client.Create(t.GetContext(), upgradePrTwo)
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error upgrading to the placeholder package revision")
}

func (t *PorchSuite) validateKptfileBasics(kptfile *kptfilev1.KptFile, expectedName string) {
	if got, want := kptfile.Name, expectedName; got != want {
		t.Errorf("%s package Kptfile.metadata.name: got %q, want %q", expectedName, got, want)
	}
	if kptfile.UpstreamLock == nil {
		t.Fatalf("%s package upstreamLock is missing", expectedName)
	}
	if kptfile.UpstreamLock.Git == nil {
		t.Errorf("%s package upstreamLock.git is missing", expectedName)
	}
	if kptfile.UpstreamLock.Git.Commit == "" {
		t.Errorf("%s package upstreamLock.git.commit is missing", expectedName)
	}
}

func (t *PorchSuite) validateUpstreamLock(kptfile *kptfilev1.KptFile, testBlueprintsRepo string) {
	got := kptfile.UpstreamLock
	got.Git.Commit = ""
	want := &kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git: &kptfilev1.GitLock{
			Repo:      testBlueprintsRepo,
			Directory: basensDir,
			Ref:       basensRef,
		},
	}
	if !cmp.Equal(want, got) {
		t.Errorf("unexpected upstreamlock returned (-want, +got) %s", cmp.Diff(want, got))
	}
}

func (t *PorchSuite) validateUpstream(kptfile *kptfilev1.KptFile, testBlueprintsRepo string) {
	got := kptfile.Upstream
	want := &kptfilev1.Upstream{
		Type: kptfilev1.GitOrigin,
		Git: &kptfilev1.Git{
			Repo:      testBlueprintsRepo,
			Directory: basensDir,
			Ref:       basensRef,
		},
	}
	if !cmp.Equal(want, got) {
		t.Errorf("unexpected upstream returned (-want, +got) %s", cmp.Diff(want, got))
	}
}
