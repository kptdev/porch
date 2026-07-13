// Copyright 2025-2026 The kpt Authors
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
	"errors"
	"fmt"
	"strings"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	kptfn "github.com/kptdev/krm-functions-sdk/go/fn"
	kptfileko "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	fakeextrepo "github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/kpt/pkg/lib/builtins/builtintypes"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockPackageRevisionDraft struct{}

func (m *mockPackageRevisionDraft) UpdateResources(ctx context.Context, resources *porchapi.PackageRevisionResources, task *porchapi.Task) error {
	return nil
}

func (m *mockPackageRevisionDraft) Key() repository.PackageRevisionKey {
	return repository.PackageRevisionKey{
		WorkspaceName: "test-revision",
		Revision:      1,
	}
}

func (m *mockPackageRevisionDraft) UpdateLifecycle(ctx context.Context, lifecycle porchapi.PackageRevisionLifecycle) error {
	return nil
}

func (m *mockPackageRevisionDraft) GetMeta() metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      "mock-package-revision",
		Namespace: "default",
	}
}

func TestApplyTasks(t *testing.T) {
	tests := []struct {
		name          string
		tasks         []porchapi.Task
		expectedError string
	}{
		{
			name: "Valid Clone task",
			tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						Upstream: porchapi.UpstreamPackage{
							Type: porchapi.RepositoryTypeGit,
							Git: &porchapi.GitPackage{
								Repo:      "https://github.com/example/repo.git",
								Ref:       "main",
								Directory: "/path/to/package",
							},
						},
					},
				},
			},
			expectedError: "cannot fetch repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &genericTaskHandler{
				runnerOptionsResolver: func(namespace string) runneroptions.RunnerOptions {
					// Return a mock RunnerOptions
					return runneroptions.RunnerOptions{
						DisplayResourceCount: false,
					}
				},
				repoOperationRetryAttempts: 3,
			}

			// Mock inputs
			ctx := context.Background()

			draft := &mockPackageRevisionDraft{}
			repositoryObj := &configapi.Repository{}
			obj := &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: tt.tasks,
				},
			}
			packageConfig := &builtintypes.PackageConfig{}

			// Call ApplyTask
			err := handler.ApplyTask(ctx, draft, repositoryObj, obj, packageConfig)

			// Verify results
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMapTaskToMutationUpgradeTask(t *testing.T) {
	th := &genericTaskHandler{}

	task := &porchapi.Task{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: "old-upstream",
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: "new-upstream",
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: "local",
			},
		},
	}

	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName: "test-package",
		},
	}

	mut, err := th.mapTaskToMutation(obj, task, false, nil)

	require.NoError(t, err)
	require.NotNil(t, mut)
	assert.IsType(t, &upgradePackageMutation{}, mut)
}

func TestDoPrMutations(t *testing.T) {
	ror := func(namespace string) runneroptions.RunnerOptions {
		return runneroptions.RunnerOptions{
			ImagePullPolicy: runneroptions.IfNotPresentPull,
		}
	}

	th := &genericTaskHandler{
		runnerOptionsResolver: ror,
	}

	repoPr := &fakeextrepo.FakePackageRevision{
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		},
	}
	repoPr.Resources.Spec.Resources[kptfilev1.KptFileName] = `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-package
`
	draft := repoPr

	t.Run("No-op when not draft", func(t *testing.T) {
		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecyclePublished,
			},
		}
		err := th.DoPRMutations(context.TODO(), repoPr, oldObj, &porchapi.PackageRevision{}, draft)
		require.NoError(t, err)
		assert.Empty(t, draft.Ops)
	})

	t.Run("Success", func(t *testing.T) {
		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		err := th.DoPRMutations(context.TODO(), repoPr, oldObj, &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeInit,
						Init: &porchapi.PackageInitTaskSpec{},
					},
				},
			},
		}, draft)
		require.NoError(t, err)
		require.NotEmpty(t, draft.Ops)
		assert.Equal(t, "UpdateResources", draft.Ops[len(draft.Ops)-1])
	})

	t.Run("No subpackage task when SubpackageDir is empty", func(t *testing.T) {
		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		// newObj has a clone task but no SubpackageDir set
		newObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{
						Type:  porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{},
					},
				},
			},
		}
		err := th.DoPRMutations(context.TODO(), repoPr, oldObj, newObj, draft)
		require.NoError(t, err)
		require.NotEmpty(t, draft.Ops)
		assert.Equal(t, "UpdateResources", draft.Ops[len(draft.Ops)-1])
	})

	t.Run("Error when SubpackageDir is set but task list is invalid", func(t *testing.T) {
		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		// newObj has a clone task with SubpackageDir but only 1 task (needs 2)
		newObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							SubpackageDir: "my-subpkg",
						},
					},
				},
			},
		}

		thWithResolver := &genericTaskHandler{
			runnerOptionsResolver: ror,
			referenceResolver:     &mockReferenceResolver{repo: &configapi.Repository{}},
		}

		err := thWithResolver.DoPRMutations(context.TODO(), repoPr, oldObj, newObj, draft)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply subpackage task")
		assert.Contains(t, err.Error(), "subpackage directory may not be specified as the first task on the task list")
	})

	t.Run("Error when SubpackageDir is invalid, is not a relative subpackageDir", func(t *testing.T) {
		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		// newObj has a clone task with SubpackageDir but only 1 task (needs 2)
		newObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							SubpackageDir: "/my-subpkg",
						},
					},
				},
			},
		}

		thWithResolver := &genericTaskHandler{
			runnerOptionsResolver: ror,
			referenceResolver:     &mockReferenceResolver{repo: &configapi.Repository{}},
		}

		err := thWithResolver.DoPRMutations(context.TODO(), repoPr, oldObj, newObj, draft)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply subpackage task")
		assert.Contains(t, err.Error(), "subpackageDir")
		assert.Contains(t, err.Error(), "is invalid")
	})

	t.Run("Success with valid SubpackageDir and proper tasks", func(t *testing.T) {
		upstreamPrKey := repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "upstream-repo",
				},
				Package: "subpkg",
			},
			WorkspaceName: "ws",
			Revision:      1,
		}

		upstreamPR := &fakeextrepo.FakePackageRevision{
			PrKey: upstreamPrKey,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						"Kptfile":       "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: subpkg\n",
						"resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sub-cm\n",
					},
				},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{
					Type: kptfilev1.GitOrigin,
					Git: &kptfilev1.Git{
						Repo:      "https://github.com/example/repo.git",
						Ref:       "main",
						Directory: "/subpkg",
					},
				},
				UpstreamLock: &kptfilev1.Locator{
					Type: kptfilev1.GitOrigin,
					Git: &kptfilev1.GitLock{
						Repo:      "https://github.com/example/repo.git",
						Ref:       "main",
						Directory: "/subpkg",
						Commit:    "abc123",
					},
				},
			},
		}

		fakeRepo := &fakeextrepo.Repository{
			PackageRevisions: []repository.PackageRevision{upstreamPR},
		}

		repoPrWithKptfile := &fakeextrepo.FakePackageRevision{
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName: "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: test-package\n",
					},
				},
			},
		}

		draftForSubpkg := &fakeextrepo.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Namespace: "default",
						Name:      "test-repo",
					},
					Package: "test-pkg",
				},
				WorkspaceName: "ws",
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{},
				},
			},
		}

		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		newObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
					{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							SubpackageDir: "my-subpkg",
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{
									Name: "upstream-repo.subpkg.ws",
								},
							},
						},
					},
				},
			},
		}

		thWithSubpkg := &genericTaskHandler{
			runnerOptionsResolver: ror,
			referenceResolver:     &mockReferenceResolver{repo: &configapi.Repository{}},
			repoOpener:            &mockRepositoryOpener{repo: fakeRepo},
		}

		err := thWithSubpkg.DoPRMutations(context.TODO(), repoPrWithKptfile, oldObj, newObj, draftForSubpkg)
		require.NoError(t, err)
		require.NotEmpty(t, draftForSubpkg.Ops)
		assert.Equal(t, "UpdateResources", draftForSubpkg.Ops[len(draftForSubpkg.Ops)-1])
	})

	t.Run("Error with SubpackageDir when reference resolver fails", func(t *testing.T) {
		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		newObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
					{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							SubpackageDir: "my-subpkg",
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{
									Name: "upstream-repo.subpkg.ws",
								},
							},
						},
					},
				},
			},
		}

		draftForErr := &fakeextrepo.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Namespace: "default",
						Name:      "test-repo",
					},
					Package: "test-pkg",
				},
				WorkspaceName: "ws",
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{},
				},
			},
		}

		thWithFailingResolver := &genericTaskHandler{
			runnerOptionsResolver: ror,
			referenceResolver:     &mockReferenceResolver{err: fmt.Errorf("repo not found")},
		}

		err := thWithFailingResolver.DoPRMutations(context.TODO(), repoPr, oldObj, newObj, draftForErr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply subpackage task")
		assert.Contains(t, err.Error(), "cannot find repository")
	})

	t.Run("Error when SubpackageDir conflicts with existing parent content", func(t *testing.T) {
		upstreamPrKey := repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "upstream-repo",
				},
				Package: "subpkg",
			},
			WorkspaceName: "ws",
			Revision:      1,
		}

		upstreamPR := &fakeextrepo.FakePackageRevision{
			PrKey: upstreamPrKey,
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: subpkg\n",
					},
				},
			},
			Kptfile: kptfilev1.KptFile{
				Upstream: &kptfilev1.Upstream{
					Type: kptfilev1.GitOrigin,
					Git:  &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "main", Directory: "/subpkg"},
				},
				UpstreamLock: &kptfilev1.Locator{
					Type: kptfilev1.GitOrigin,
					Git:  &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "main", Directory: "/subpkg", Commit: "abc123"},
				},
			},
		}

		fakeRepo := &fakeextrepo.Repository{
			PackageRevisions: []repository.PackageRevision{upstreamPR},
		}

		// Parent already has content at the target SubpackageDir
		repoPrWithConflict := &fakeextrepo.FakePackageRevision{
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{
						kptfilev1.KptFileName:     "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: parent\n",
						"my-subpkg/existing.yaml": "existing content",
					},
				},
			},
		}

		draftForConflict := &fakeextrepo.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Namespace: "default",
						Name:      "test-repo",
					},
					Package: "test-pkg",
				},
				WorkspaceName: "ws",
			},
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{},
				},
			},
		}

		oldObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		}
		newObj := &porchapi.PackageRevision{
			Spec: porchapi.PackageRevisionSpec{
				Tasks: []porchapi.Task{
					{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
					{
						Type: porchapi.TaskTypeClone,
						Clone: &porchapi.PackageCloneTaskSpec{
							SubpackageDir: "my-subpkg",
							Upstream: porchapi.UpstreamPackage{
								UpstreamRef: &porchapi.PackageRevisionRef{
									Name: "upstream-repo.subpkg.ws",
								},
							},
						},
					},
				},
			},
		}

		thConflict := &genericTaskHandler{
			runnerOptionsResolver: ror,
			referenceResolver:     &mockReferenceResolver{repo: &configapi.Repository{}},
			repoOpener:            &mockRepositoryOpener{repo: fakeRepo},
		}

		err := thConflict.DoPRMutations(context.TODO(), repoPrWithConflict, oldObj, newObj, draftForConflict)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply subpackage task")
		assert.Contains(t, err.Error(), "cannot clone subpackage into parent, parent already has content at")
	})
}

func TestDoPrResourceMutations(t *testing.T) {
	ror := func(namespace string) runneroptions.RunnerOptions {
		return runneroptions.RunnerOptions{
			ImagePullPolicy: runneroptions.IfNotPresentPull,
		}
	}

	th := &genericTaskHandler{
		runnerOptionsResolver: ror,
		runtime:               NewSimpleFunctionRuntime(),
	}

	repoPr := &fakeextrepo.FakePackageRevision{
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		},
	}
	draft := repoPr

	t.Run("Empty resources", func(t *testing.T) {
		oldRes := &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		}
		newRes := oldRes.DeepCopy()
		renderStatus, err := th.DoPRResourceMutations(context.TODO(), repoPr, draft, oldRes, newRes)
		require.NoError(t, err)
		assert.Nil(t, renderStatus)
		require.NotEmpty(t, draft.Ops)
		assert.Equal(t, "UpdateResources", draft.Ops[len(draft.Ops)-1])
		assert.Empty(t, draft.Resources.Spec.Resources)
	})

	t.Run("Basic resources", func(t *testing.T) {
		oldRes := &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"foo.txt": "bar",
				},
			},
		}
		newRes := oldRes.DeepCopy()
		newRes.Spec.Resources["baz.txt"] = "bar"
		renderStatus, err := th.DoPRResourceMutations(context.TODO(), repoPr, draft, oldRes, newRes)
		require.NoError(t, err)
		assert.Nil(t, renderStatus)
		require.NotEmpty(t, draft.Ops)
		assert.Equal(t, "UpdateResources", draft.Ops[len(draft.Ops)-1])
		assert.Equal(t, map[string]string{
			"foo.txt": "bar",
			"baz.txt": "bar",
		}, draft.Resources.Spec.Resources)
	})

	t.Run("Render failure persists resources to draft", func(t *testing.T) {
		kptfileContent := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
pipeline:
  mutators:
    - image: quay.io/invalid/nonexistent-fn:v0.0.1
`
		draft := &fakeextrepo.FakePackageRevision{
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{},
				},
			},
		}
		oldRes := &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile": kptfileContent,
				},
			},
		}
		newRes := oldRes.DeepCopy()
		newRes.Spec.Resources["configmap.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n"

		renderStatus, err := th.DoPRResourceMutations(context.TODO(), repoPr, draft, oldRes, newRes)
		require.Error(t, err)
		assert.NotNil(t, renderStatus)
		// Should return a typed RenderError
		var renderError *RenderError
		require.True(t, errors.As(err, &renderError))
		// Verify resources were written to draft despite render failure
		require.Contains(t, draft.Ops, "UpdateResources")
		assert.Contains(t, draft.Resources.Spec.Resources, "configmap.yaml")
	})

	t.Run("Render failure with draft update error", func(t *testing.T) {
		kptfileContent := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
pipeline:
  mutators:
    - image: quay.io/invalid/nonexistent-fn:v0.0.1
`
		draft := &fakeextrepo.FakePackageRevision{
			Err: fmt.Errorf("draft update failed"),
			Resources: &porchapi.PackageRevisionResources{
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{},
				},
			},
		}
		oldRes := &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile": kptfileContent,
				},
			},
		}
		newRes := oldRes.DeepCopy()

		renderStatus, err := th.DoPRResourceMutations(context.TODO(), repoPr, draft, oldRes, newRes)
		require.Error(t, err)
		assert.NotNil(t, renderStatus)
		// Should return a typed RenderPersistError
		var persistErr *RenderPersistError
		require.True(t, errors.As(err, &persistErr))
		assert.Contains(t, persistErr.PersistErr.Error(), "draft update failed")
		assert.NotNil(t, persistErr.RenderErr)
	})
}

func TestRenderError(t *testing.T) {
	baseErr := errors.New("some base error")
	wrappedErr := renderError(baseErr)

	if wrappedErr == nil {
		t.Fatal("expected non-nil error")
	}

	got := wrappedErr.Error()

	if !strings.Contains(got, "some base error") {
		t.Errorf("expected base error message to be included, got: %q", got)
	}

	if !strings.Contains(got, "Package NOT pushed to remote") {
		t.Errorf("expected 'NOT pushed' message, got: %q", got)
	}
}

func TestKptfilePreservesComments(t *testing.T) {
	// Kptfile with labels/annotations present
	originalKptfile := `
apiVersion: kpt.dev/v1
kind: Kptfile
metadata: # kpt-merge: /my-pkg
  name: my-pkg # this is the package name
  labels:
    foo: bar # upstream label
    toremove: will-be-removed # label to be removed
  annotations:
    foo: bar # upstream annotation
    toremove: will-be-removed # annotation to be removed
# Top-level comment
info:
  readinessGates:
    # This gate is important
    - conditionType: Ready # readiness gate comment
`

	resources := map[string]string{
		"Kptfile": originalKptfile,
	}

	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageMetadata: &porchapi.PackageMetadata{
				Labels: map[string]string{
					"new-label": "new-label-value",
					"foo":       "bar-updated",
				},
				Annotations: map[string]string{
					"new-annotation": "new-annotation-value",
					"foo":            "bar-updated",
				},
			},
			ReadinessGates: []porchapi.ReadinessGate{
				{ConditionType: "Ready"},
				{ConditionType: "Healthy"},
			},
		},
	}

	kptf, err := kptfileko.NewFromPackage(resources)
	require.NoError(t, err)

	kptf.SetLabels(obj.Spec.PackageMetadata.Labels)
	labelsAfter := kptf.GetLabels()
	assert.Equal(t, "bar-updated", labelsAfter["foo"])
	assert.Equal(t, "new-label-value", labelsAfter["new-label"])
	assert.NotContains(t, labelsAfter, "toremove")

	kptf.SetAnnotations(obj.Spec.PackageMetadata.Annotations)
	annotationsAfter := kptf.GetAnnotations()
	assert.Equal(t, "bar-updated", annotationsAfter["foo"])
	assert.Equal(t, "new-annotation-value", annotationsAfter["new-annotation"])
	assert.NotContains(t, annotationsAfter, "toremove")

	var gates kptfn.SliceSubObjects
	for _, rg := range obj.Spec.ReadinessGates {
		ko, err := kptfn.NewFromTypedObject(rg)
		require.NoError(t, err)
		gates = append(gates, &ko.SubObject)
	}
	require.NoError(t, kptf.SetReadinessGates(gates))

	require.NoError(t, kptf.WriteToPackage(resources))
	got := resources["Kptfile"]

	assert.Contains(t, got, "# this is the package name")
	assert.Contains(t, got, "# upstream label")
	assert.Contains(t, got, "# upstream annotation")
	assert.Contains(t, got, "# Top-level comment")
	assert.Contains(t, got, "# This gate is important")
	assert.Contains(t, got, "# readiness gate comment")

	// Kptfile with labels/annotations initially empty
	emptyKptfile := `
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-pkg
# Top-level comment
info:
  readinessGates:
    - conditionType: Ready
`

	resources2 := map[string]string{
		"Kptfile": emptyKptfile,
	}

	obj2 := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageMetadata: &porchapi.PackageMetadata{
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"foo": "bar",
				},
			},
		},
	}
	kptf2, err := kptfileko.NewFromPackage(resources2)
	require.NoError(t, err)

	labels2 := kptf2.GetLabels()
	for k, v := range obj2.Spec.PackageMetadata.Labels {
		labels2[k] = v
	}
	kptf2.SetLabels(labels2)

	annotations2 := kptf2.GetAnnotations()
	for k, v := range obj2.Spec.PackageMetadata.Annotations {
		annotations2[k] = v
	}
	kptf2.SetAnnotations(annotations2)
	require.NoError(t, kptf2.WriteToPackage(resources2))
	got2 := resources2["Kptfile"]
	assert.Contains(t, got2, "foo: bar")
	assert.Contains(t, got2, "# Top-level comment")
}

func TestApplySubpackageTask(t *testing.T) {
	tests := []struct {
		name          string
		obj           *porchapi.PackageRevision
		resources     repository.PackageResources
		resolveErr    error
		expectedError string
	}{
		{
			name: "Error when task list does not contain exactly 2 tasks",
			obj: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeClone},
					},
				},
			},
			resources:     repository.PackageResources{Contents: map[string]string{}},
			expectedError: "task list must contain exactly 2 tasks",
		},
		{
			name: "Error when reference resolver fails",
			obj: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
						{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{
							SubpackageDir: "subpkg",
							Upstream: porchapi.UpstreamPackage{
								Type: porchapi.RepositoryTypeGit,
								Git: &porchapi.GitPackage{
									Repo: "https://github.com/example/repo.git",
									Ref:  "main",
								},
							},
						}},
					},
				},
			},
			resources:     repository.PackageResources{Contents: map[string]string{}},
			resolveErr:    fmt.Errorf("repository not found"),
			expectedError: "cannot find repository",
		},
		{
			name: "Error when second task has unsupported type",
			obj: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
						{Type: porchapi.TaskTypeRender},
					},
				},
			},
			resources:     repository.PackageResources{Contents: map[string]string{}},
			expectedError: "not supported",
		},
		{
			name: "Error when second task clone field is nil",
			obj: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
						{Type: porchapi.TaskTypeClone, Clone: nil},
					},
				},
			},
			resources:     repository.PackageResources{Contents: map[string]string{}},
			expectedError: "clone not set for task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := &fakeextrepo.FakePackageRevision{
				PrKey: repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{
							Namespace: "default",
							Name:      "test-repo",
						},
						Package: "test-pkg",
					},
					WorkspaceName: "ws",
				},
				Resources: &porchapi.PackageRevisionResources{
					Spec: porchapi.PackageRevisionResourcesSpec{
						Resources: map[string]string{},
					},
				},
			}

			th := &genericTaskHandler{
				referenceResolver: &mockReferenceResolver{err: tt.resolveErr},
			}

			err := th.applySubpackageTask(context.Background(), draft, tt.obj, tt.resources)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestApplySubpackageTask_SuccessfulClone(t *testing.T) {
	// The upstream package revision key: repo="upstream-repo", package="subpkg", workspace="ws"
	// This produces the k8s name: "upstream-repo.subpkg.ws"
	upstreamPrKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace: "default",
				Name:      "upstream-repo",
			},
			Package: "subpkg",
		},
		WorkspaceName: "ws",
		Revision:      1,
	}

	upstreamResources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile":       "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: subpkg\n",
				"resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n",
			},
		},
	}

	upstreamPR := &fakeextrepo.FakePackageRevision{
		PrKey:     upstreamPrKey,
		Resources: upstreamResources,
		Kptfile: kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git: &kptfilev1.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "main",
					Directory: "/subpkg",
				},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git: &kptfilev1.GitLock{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "main",
					Directory: "/subpkg",
					Commit:    "abc123",
				},
			},
		},
	}

	fakeRepo := &fakeextrepo.Repository{
		PackageRevisions: []repository.PackageRevision{upstreamPR},
	}

	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						SubpackageDir: "my-subpkg",
						Upstream: porchapi.UpstreamPackage{
							UpstreamRef: &porchapi.PackageRevisionRef{
								Name: "upstream-repo.subpkg.ws",
							},
						},
					},
				},
			},
		},
	}

	parentResources := repository.PackageResources{
		Contents: map[string]string{
			"Kptfile":     "parent-kptfile",
			"parent.yaml": "parent-resource",
		},
	}

	draft := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "test-repo",
				},
				Package: "test-pkg",
			},
			WorkspaceName: "ws",
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		},
	}

	th := &genericTaskHandler{
		referenceResolver: &mockReferenceResolver{
			repo: &configapi.Repository{},
		},
		repoOpener: &mockRepositoryOpener{repo: fakeRepo},
	}

	err := th.applySubpackageTask(context.Background(), draft, obj, parentResources)

	require.NoError(t, err)
	// Verify subpackage resources were inserted into parent
	assert.Equal(t, "parent-kptfile", parentResources.Contents["Kptfile"])
	assert.Equal(t, "parent-resource", parentResources.Contents["parent.yaml"])
	assert.Contains(t, parentResources.Contents, "my-subpkg/Kptfile")
	assert.Contains(t, parentResources.Contents, "my-subpkg/resource.yaml")
	// Verify tasks were trimmed to only the first task
	assert.Len(t, obj.Spec.Tasks, 1)
	assert.Equal(t, porchapi.TaskTypeClone, obj.Spec.Tasks[0].Type)
}

func TestApplySubpackageTask_SuccessfulUpgrade(t *testing.T) {
	kptfileContent := "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: subpkg\n"

	// Old upstream package revision (the original version of the subpackage)
	oldUpstreamPR := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "upstream-repo",
				},
				Package: "subpkg",
			},
			WorkspaceName: "v1",
			Revision:      1,
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile":       kptfileContent,
					"resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: original\n",
				},
			},
		},
	}

	// New upstream package revision (the upgraded version)
	newUpstreamPR := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "upstream-repo",
				},
				Package: "subpkg",
			},
			WorkspaceName: "v2",
			Revision:      2,
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile":       kptfileContent,
					"resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: upgraded\n",
				},
			},
		},
		Kptfile: kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git: &kptfilev1.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "v2",
					Directory: "/subpkg",
				},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git: &kptfilev1.GitLock{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "v2",
					Directory: "/subpkg",
					Commit:    "def456",
				},
			},
		},
	}

	// Local package revision (contains the subpackage at "my-subpkg/")
	localPR := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "upstream-repo",
				},
				Package: "subpkg",
			},
			WorkspaceName: "local",
			Revision:      1,
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile":                 "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: parent\n",
					"my-subpkg/Kptfile":       kptfileContent,
					"my-subpkg/resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: original\n",
				},
			},
		},
	}

	fakeRepo := &fakeextrepo.Repository{
		PackageRevisions: []repository.PackageRevision{oldUpstreamPR, newUpstreamPR, localPR},
	}

	// Parent resources with existing subpackage content at "my-subpkg/"
	parentResources := repository.PackageResources{
		Contents: map[string]string{
			"Kptfile":                 "parent-kptfile",
			"my-subpkg/Kptfile":       kptfileContent,
			"my-subpkg/resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: original\n",
		},
	}

	draft := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "test-repo",
				},
				Package: "test-pkg",
			},
			WorkspaceName: "ws",
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		},
	}

	th := &genericTaskHandler{
		referenceResolver: &mockReferenceResolver{
			repo: &configapi.Repository{},
		},
		repoOpener: &mockRepositoryOpener{repo: fakeRepo},
	}

	// Second task is TaskTypeUpgrade to exercise the upgrade branch
	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
				{
					Type: porchapi.TaskTypeUpgrade,
					Upgrade: &porchapi.PackageUpgradeTaskSpec{
						SubpackageDir: "my-subpkg",
						OldUpstream: porchapi.PackageRevisionRef{
							Name: "upstream-repo.subpkg.v1",
						},
						NewUpstream: porchapi.PackageRevisionRef{
							Name: "upstream-repo.subpkg.v2",
						},
						LocalPackageRevisionRef: porchapi.PackageRevisionRef{
							Name: "upstream-repo.subpkg.local",
						},
					},
				},
			},
		},
	}

	err := th.applySubpackageTask(context.Background(), draft, obj, parentResources)
	require.NoError(t, err)

	// Verify old subpackage resources were replaced with upgraded content
	assert.Contains(t, parentResources.Contents, "my-subpkg/Kptfile")
	assert.Contains(t, parentResources.Contents, "my-subpkg/resource.yaml")
	// Verify parent resources are preserved
	assert.Equal(t, "parent-kptfile", parentResources.Contents["Kptfile"])
	// Verify tasks were trimmed to only the first source task
	assert.Len(t, obj.Spec.Tasks, 1)
	assert.Equal(t, porchapi.TaskTypeClone, obj.Spec.Tasks[0].Type)
}

func TestApplySubpackageTask_ClearsTasksAfterExecution(t *testing.T) {
	upstreamPrKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace: "default",
				Name:      "upstream-repo",
			},
			Package: "subpkg",
		},
		WorkspaceName: "ws",
		Revision:      1,
	}

	upstreamPR := &fakeextrepo.FakePackageRevision{
		PrKey: upstreamPrKey,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: subpkg\n",
				},
			},
		},
		Kptfile: kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git: &kptfilev1.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "main",
					Directory: "/subpkg",
				},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git: &kptfilev1.GitLock{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "main",
					Directory: "/subpkg",
					Commit:    "abc123",
				},
			},
		},
	}

	fakeRepo := &fakeextrepo.Repository{
		PackageRevisions: []repository.PackageRevision{upstreamPR},
	}

	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						SubpackageDir: "subpkg",
						Upstream: porchapi.UpstreamPackage{
							UpstreamRef: &porchapi.PackageRevisionRef{
								Name: "upstream-repo.subpkg.ws",
							},
						},
					},
				},
			},
		},
	}

	draft := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "test-repo",
				},
				Package: "test-pkg",
			},
			WorkspaceName: "ws",
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		},
	}

	th := &genericTaskHandler{
		referenceResolver: &mockReferenceResolver{
			repo: &configapi.Repository{},
		},
		repoOpener: &mockRepositoryOpener{repo: fakeRepo},
	}

	err := th.applySubpackageTask(context.Background(), draft, obj, repository.PackageResources{Contents: map[string]string{}})
	require.NoError(t, err)

	// Verify tasks were trimmed to only the first task
	assert.Len(t, obj.Spec.Tasks, 1)
	assert.Equal(t, porchapi.TaskTypeClone, obj.Spec.Tasks[0].Type)
}

func TestApplySubpackageTask_InvalidSubpackageName(t *testing.T) {
	// SubpackageDir that produces an invalid k8s name (uppercase letters)
	upstreamPrKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace: "default",
				Name:      "upstream-repo",
			},
			Package: "subpkg",
		},
		WorkspaceName: "ws",
		Revision:      1,
	}

	upstreamPR := &fakeextrepo.FakePackageRevision{
		PrKey: upstreamPrKey,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: subpkg\n",
				},
			},
		},
		Kptfile: kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.Git{Repo: "https://github.com/example/repo.git", Ref: "main", Directory: "/subpkg"},
			},
			UpstreamLock: &kptfilev1.Locator{
				Type: kptfilev1.GitOrigin,
				Git:  &kptfilev1.GitLock{Repo: "https://github.com/example/repo.git", Ref: "main", Directory: "/subpkg", Commit: "abc123"},
			},
		},
	}

	fakeRepo := &fakeextrepo.Repository{
		PackageRevisions: []repository.PackageRevision{upstreamPR},
	}

	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{Type: porchapi.TaskTypeClone, Clone: &porchapi.PackageCloneTaskSpec{}},
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						SubpackageDir: "INVALID_UPPERCASE",
						Upstream: porchapi.UpstreamPackage{
							UpstreamRef: &porchapi.PackageRevisionRef{
								Name: "upstream-repo.subpkg.ws",
							},
						},
					},
				},
			},
		},
	}

	draft := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "default",
					Name:      "test-repo",
				},
				Package: "test-pkg",
			},
			WorkspaceName: "ws",
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{},
			},
		},
	}

	th := &genericTaskHandler{
		referenceResolver: &mockReferenceResolver{repo: &configapi.Repository{}},
		repoOpener:        &mockRepositoryOpener{repo: fakeRepo},
	}

	err := th.applySubpackageTask(context.Background(), draft, obj, repository.PackageResources{Contents: map[string]string{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subpackage resource name")
	assert.Contains(t, err.Error(), "lowercase RFC 1123 subdomain")
}

type mockReferenceResolver struct {
	repo *configapi.Repository
	err  error
}

func (m *mockReferenceResolver) ResolveReference(_ context.Context, _, _ string, result repository.Object) error {
	if m.err != nil {
		return m.err
	}
	if m.repo != nil {
		repo, ok := result.(*configapi.Repository)
		if ok {
			*repo = *m.repo
		}
	}
	return nil
}

type mockRepositoryOpener struct {
	repo repository.Repository
	err  error
}

func (m *mockRepositoryOpener) OpenRepository(_ context.Context, _ *configapi.Repository) (repository.Repository, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.repo, nil
}

func TestUpgradeSubpackageResourcesInDraftResources(t *testing.T) {
	th := &genericTaskHandler{}

	tests := []struct {
		name             string
		subpackageDir    string
		parentResources  map[string]string
		subpkgResources  map[string]string
		expectedError    string
		expectedContents map[string]string
	}{
		{
			name:          "Successfully upgrades existing subpackage resources",
			subpackageDir: "subpkg",
			parentResources: map[string]string{
				"Kptfile":              "parent-kptfile",
				"subpkg/Kptfile":       "old-subpkg-kptfile",
				"subpkg/resource.yaml": "old-resource",
			},
			subpkgResources: map[string]string{
				"Kptfile":           "new-subpkg-kptfile",
				"new-resource.yaml": "new-resource",
			},
			expectedContents: map[string]string{
				"Kptfile":                  "parent-kptfile",
				"subpkg/Kptfile":           "new-subpkg-kptfile",
				"subpkg/new-resource.yaml": "new-resource",
			},
		},
		{
			name:          "Error when subpackage does not exist in parent",
			subpackageDir: "nonexistent",
			parentResources: map[string]string{
				"Kptfile":     "parent-kptfile",
				"config.yaml": "config",
			},
			subpkgResources: map[string]string{
				"Kptfile": "subpkg-kptfile",
			},
			expectedError: "parent does not have a subpackage at",
		},
		{
			name:          "Error when subpackage dir is inside an existing subpackage",
			subpackageDir: "sub/subsub/subsubsub",
			parentResources: map[string]string{
				"Kptfile":           "parent-kptfile",
				"sub/Kptfile":       "kptfile for existing subpackage",
				"sub/resource.yaml": "existing-content",
				"sub/existing.yaml": "existing-content",
			},
			subpkgResources: map[string]string{
				"Kptfile": "subpkg-kptfile",
			},
			expectedError: "cannot upgrade subpackage in another subpackage, parent already has a subpackage at",
		},
		{
			name:          "Error when subpackage dir is inside an existing subsubpackage",
			subpackageDir: "sub/subsub/subsubsub",
			parentResources: map[string]string{
				"Kptfile":                  "parent-kptfile",
				"sub/subsub/Kptfile":       "kptfile for existing subpackage",
				"sub/subsub/resource.yaml": "existing-content",
				"sub/subsub/existing.yaml": "existing-content",
			},
			subpkgResources: map[string]string{
				"Kptfile": "subpkg-kptfile",
			},
			expectedError: "cannot upgrade subpackage in another subpackage, parent already has a subpackage at",
		},
		{
			name:          "Successfully upgrades nested subpackage dir",
			subpackageDir: "path/to/subpkg",
			parentResources: map[string]string{
				"Kptfile":                    "parent-kptfile",
				"path/to/subpkg/Kptfile":     "old-nested-kptfile",
				"path/to/subpkg/deploy.yaml": "old-deploy",
			},
			subpkgResources: map[string]string{
				"Kptfile":      "new-nested-kptfile",
				"service.yaml": "new-service",
			},
			expectedContents: map[string]string{
				"Kptfile":                     "parent-kptfile",
				"path/to/subpkg/Kptfile":      "new-nested-kptfile",
				"path/to/subpkg/service.yaml": "new-service",
			},
		},
		{
			name:          "Old subpackage resources are fully replaced",
			subpackageDir: "subpkg",
			parentResources: map[string]string{
				"Kptfile":              "parent-kptfile",
				"subpkg/old1.yaml":     "old1",
				"subpkg/old2.yaml":     "old2",
				"subpkg/sub/old3.yaml": "old3",
			},
			subpkgResources: map[string]string{
				"new1.yaml": "new1",
			},
			expectedContents: map[string]string{
				"Kptfile":          "parent-kptfile",
				"subpkg/new1.yaml": "new1",
			},
		},
		{
			name:          "Upgrade with empty new subpackage resources removes old content",
			subpackageDir: "subpkg",
			parentResources: map[string]string{
				"Kptfile":              "parent-kptfile",
				"subpkg/Kptfile":       "old-kptfile",
				"subpkg/resource.yaml": "old-resource",
			},
			subpkgResources: map[string]string{},
			expectedContents: map[string]string{
				"Kptfile": "parent-kptfile",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parentRes := repository.PackageResources{Contents: tt.parentResources}
			subpkgRes := repository.PackageResources{Contents: tt.subpkgResources}

			err := th.upgradeSubpackageResourcesInDraftResources(context.Background(), tt.subpackageDir, parentRes, subpkgRes)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedContents, parentRes.Contents)
			}
		})
	}
}

func TestInsertSubpackageResourcesInDraftResources(t *testing.T) {
	th := &genericTaskHandler{}

	tests := []struct {
		name             string
		subpackageDir    string
		parentResources  map[string]string
		subpkgResources  map[string]string
		expectedError    string
		expectedContents map[string]string
	}{
		{
			name:          "Successfully inserts subpackage resources into empty parent",
			subpackageDir: "subpkg",
			parentResources: map[string]string{
				"Kptfile":     "parent-kptfile",
				"config.yaml": "parent-config",
			},
			subpkgResources: map[string]string{
				"Kptfile":       "subpkg-kptfile",
				"resource.yaml": "subpkg-resource",
			},
			expectedContents: map[string]string{
				"Kptfile":              "parent-kptfile",
				"config.yaml":          "parent-config",
				"subpkg/Kptfile":       "subpkg-kptfile",
				"subpkg/resource.yaml": "subpkg-resource",
			},
		},
		{
			name:          "Error when parent already has content at subpackage dir",
			subpackageDir: "subpkg",
			parentResources: map[string]string{
				"Kptfile":              "parent-kptfile",
				"subpkg/existing.yaml": "existing-content",
			},
			subpkgResources: map[string]string{
				"Kptfile": "subpkg-kptfile",
			},
			expectedError: "cannot clone subpackage into parent, parent already has content at",
		},
		{
			name:          "Error when subpackage dir is inside an existing subpackage",
			subpackageDir: "sub/subsub/subsubsub",
			parentResources: map[string]string{
				"Kptfile":           "parent-kptfile",
				"sub/Kptfile":       "kptfile for existing subpackage",
				"sub/resource.yaml": "existing-content",
				"sub/existing.yaml": "existing-content",
			},
			subpkgResources: map[string]string{
				"Kptfile": "subpkg-kptfile",
			},
			expectedError: "cannot clone subpackage into another subpackage, parent already has a subpackage at",
		},
		{
			name:          "Error when subpackage dir is inside an existing subsubpackage",
			subpackageDir: "sub/subsub/subsubsub",
			parentResources: map[string]string{
				"Kptfile":                  "parent-kptfile",
				"sub/subsub/Kptfile":       "kptfile for existing subpackage",
				"sub/subsub/resource.yaml": "existing-content",
				"sub/subsub/existing.yaml": "existing-content",
			},
			subpkgResources: map[string]string{
				"Kptfile": "subpkg-kptfile",
			},
			expectedError: "cannot clone subpackage into another subpackage, parent already has a subpackage at",
		},
		{
			name:          "Successfully inserts into nested subpackage dir",
			subpackageDir: "path/to/subpkg",
			parentResources: map[string]string{
				"Kptfile": "parent-kptfile",
			},
			subpkgResources: map[string]string{
				"Kptfile":     "nested-kptfile",
				"deploy.yaml": "deploy-content",
			},
			expectedContents: map[string]string{
				"Kptfile":                    "parent-kptfile",
				"path/to/subpkg/Kptfile":     "nested-kptfile",
				"path/to/subpkg/deploy.yaml": "deploy-content",
			},
		},
		{
			name:            "Empty subpackage resources results in no additions",
			subpackageDir:   "subpkg",
			parentResources: map[string]string{"Kptfile": "parent-kptfile"},
			subpkgResources: map[string]string{},
			expectedContents: map[string]string{
				"Kptfile": "parent-kptfile",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parentRes := repository.PackageResources{Contents: tt.parentResources}
			subpkgRes := repository.PackageResources{Contents: tt.subpkgResources}

			err := th.insertSubpackageResourcesInDraftResources(context.Background(), tt.subpackageDir, parentRes, subpkgRes)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedContents, parentRes.Contents)
			}
		})
	}
}

func TestParentSubpackageFound(t *testing.T) {
	th := &genericTaskHandler{}

	tests := []struct {
		name          string
		subpackageDir string
		resourceKey   string
		expected      string
	}{
		{
			name:          "Exact match - Kptfile at subpackageDir",
			subpackageDir: "sub",
			resourceKey:   "sub/Kptfile",
			expected:      "sub",
		},
		{
			name:          "Parent of subpackageDir has Kptfile",
			subpackageDir: "sub/nested",
			resourceKey:   "sub/Kptfile",
			expected:      "sub",
		},
		{
			name:          "Deeply nested parent has Kptfile",
			subpackageDir: "a/b/c/d",
			resourceKey:   "a/Kptfile",
			expected:      "a",
		},
		{
			name:          "Intermediate parent has Kptfile",
			subpackageDir: "a/b/c/d",
			resourceKey:   "a/b/Kptfile",
			expected:      "a/b",
		},
		{
			name:          "No match - resource is not a Kptfile",
			subpackageDir: "sub",
			resourceKey:   "sub/resource.yaml",
			expected:      "",
		},
		{
			name:          "No match - Kptfile in unrelated directory",
			subpackageDir: "sub/nested",
			resourceKey:   "other/Kptfile",
			expected:      "",
		},
		{
			name:          "No match - Kptfile in sibling directory",
			subpackageDir: "sub/nested",
			resourceKey:   "sub/other/Kptfile",
			expected:      "",
		},
		{
			name:          "No match - Kptfile deeper than subpackageDir",
			subpackageDir: "sub",
			resourceKey:   "sub/nested/Kptfile",
			expected:      "",
		},
		{
			name:          "No match - root Kptfile",
			subpackageDir: "sub",
			resourceKey:   "Kptfile",
			expected:      "",
		},
		{
			name:          "No match - similar prefix but not parent",
			subpackageDir: "sub/nested",
			resourceKey:   "sub/nested-other/Kptfile",
			expected:      "",
		},
		{
			name:          "No match - partial directory name overlap",
			subpackageDir: "subpkg",
			resourceKey:   "sub/Kptfile",
			expected:      "",
		},
		{
			name:          "Exact match - multi-level subpackageDir",
			subpackageDir: "path/to/subpkg",
			resourceKey:   "path/to/subpkg/Kptfile",
			expected:      "path/to/subpkg",
		},
		{
			name:          "Parent match - multi-level",
			subpackageDir: "path/to/subpkg",
			resourceKey:   "path/to/Kptfile",
			expected:      "path/to",
		},
		{
			name:          "Parent match - top-level parent of multi-level",
			subpackageDir: "path/to/subpkg",
			resourceKey:   "path/Kptfile",
			expected:      "path",
		},
		{
			name:          "No match - non-yaml file with Kptfile in name",
			subpackageDir: "sub",
			resourceKey:   "sub/notKptfile",
			expected:      "",
		},
		{
			name:          "No match - Kptfile as prefix of filename",
			subpackageDir: "sub",
			resourceKey:   "sub/Kptfile.bak",
			expected:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := th.parentSubpackageFound(tt.subpackageDir, tt.resourceKey)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestApplyMapMetadata(t *testing.T) {
	tests := []struct {
		name    string
		cur     map[string]string
		desired map[string]string
		replace bool
		want    bool
		wantMap map[string]string
	}{
		{
			name:    "replace mode - no change",
			cur:     map[string]string{"a": "1"},
			desired: map[string]string{"a": "1"},
			replace: true,
			want:    false,
			wantMap: map[string]string{"a": "1"},
		},
		{
			name:    "replace mode - with change",
			cur:     map[string]string{"a": "1"},
			desired: map[string]string{"b": "2"},
			replace: true,
			want:    true,
			wantMap: map[string]string{"b": "2"},
		},
		{
			name:    "merge mode - no change",
			cur:     map[string]string{"a": "1"},
			desired: map[string]string{"a": "1"},
			replace: false,
			want:    false,
			wantMap: map[string]string{"a": "1"},
		},
		{
			name:    "merge mode - add new key",
			cur:     map[string]string{"a": "1"},
			desired: map[string]string{"b": "2"},
			replace: false,
			want:    true,
			wantMap: map[string]string{"a": "1", "b": "2"},
		},
		{
			name:    "merge mode - update existing",
			cur:     map[string]string{"a": "1"},
			desired: map[string]string{"a": "2"},
			replace: false,
			want:    true,
			wantMap: map[string]string{"a": "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result map[string]string
			got := applyMapMetadata(tt.cur, tt.desired, tt.replace, func(m map[string]string) {
				result = m
			})
			assert.Equal(t, tt.want, got)
			if got {
				assert.Equal(t, tt.wantMap, result)
			}
		})
	}
}
