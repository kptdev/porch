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

package upgrade

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func makeWsName(revision int) string {
	if revision == -1 {
		return "main"
	}
	if revision > 0 {
		return fmt.Sprintf("v%d", revision)
	}
	return fmt.Sprintf("ws%d", rand.Int()%10000)
}

func setLifecycleAndName(pr *porchapi.PackageRevision, revision int) {
	if revision == 0 {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDraft
		pr.Status.UpstreamLock = &porchapi.Locator{
			Git: &porchapi.GitLock{
				Ref:  fmt.Sprintf("/drafts/%s/%s", pr.Spec.PackageName, pr.Spec.WorkspaceName),
				Repo: "https://github.com/user/repo",
			},
		}
	} else {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
		pr.Status.UpstreamLock = &porchapi.Locator{
			Git: &porchapi.GitLock{
				Ref:  fmt.Sprintf("/%s/v%d", pr.Spec.PackageName, pr.Spec.Revision),
				Repo: "https://github.com/user/repo",
			},
		}
	}

	pr.Name = repository.ComposePkgRevObjName(repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace: pr.Namespace,
				Name:      pr.Spec.RepositoryName,
			},
			Package: pr.Spec.PackageName,
		},
		Revision:      revision,
		WorkspaceName: pr.Spec.WorkspaceName,
	})
}

func createOrigPackageRevision(namespace, repo, pkgName string, revision int) *porchapi.PackageRevision {
	pr := &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: "porch.kpt.dev/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repo,
			PackageName:    pkgName,
			Revision:       revision,
			WorkspaceName:  makeWsName(revision),
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{},
				},
			},
		},
		Status: porchapi.PackageRevisionStatus{},
	}

	setLifecycleAndName(pr, revision)

	return pr
}

func createEditPackageRevision(pr *porchapi.PackageRevision, revision int) *porchapi.PackageRevision {
	newPr := pr.DeepCopy()
	newPr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: pr.Name,
				},
			},
		},
	}
	newPr.Spec.Revision = revision
	newPr.Spec.WorkspaceName = makeWsName(revision)

	setLifecycleAndName(newPr, revision)

	return newPr
}

func createClonePackageRevision(pr *porchapi.PackageRevision, pkgName string, revision int) *porchapi.PackageRevision {
	newPr := pr.DeepCopy()
	newPr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{
						Name: pr.Name,
					},
				},
			},
		},
	}
	newPr.Spec.PackageName = pkgName
	newPr.Spec.Revision = revision
	newPr.Spec.WorkspaceName = makeWsName(revision)

	setLifecycleAndName(newPr, revision)

	return newPr
}

func createRunner(ctx context.Context, c client.Client, prs []porchapi.PackageRevision, ns string, revision int) *runner {
	return &runner{
		ctx:      ctx,
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		client:   c,
		Command:  NewCommand(ctx, &genericclioptions.ConfigFlags{Namespace: &ns}),
		revision: revision,
		prs:      prs,
	}
}

func createRunnerWithDiscovery(ctx context.Context, c client.Client, prs []porchapi.PackageRevision, ns, discover string, revision int) *runner {
	rnr := createRunner(ctx, c, prs, ns, revision)
	rnr.discover = discover
	return rnr
}

func TestPreRun(t *testing.T) {
	const ns = "ns"

	orig := createOrigPackageRevision(ns, "repo", "orig", 1)
	clone := createClonePackageRevision(orig, "clone", 1)
	prs := []porchapi.PackageRevision{*orig, *clone}
	r := createRunner(context.Background(), fake.NewClientBuilder().Build(), prs, ns, 2)
	err := r.preRunE(r.Command, []string{"arg1", "arg2", "arg3"})
	assert.Error(t, err)

	err = r.preRunE(r.Command, []string{})
	assert.Error(t, err)

	r = createRunner(context.Background(), fake.NewClientBuilder().Build(), prs, ns, 2)
	err = r.preRunE(r.Command, []string{"clone"})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "workspace")
}

func TestUpgradeCommand(t *testing.T) {
	ctx := context.Background()

	origRevision := createOrigPackageRevision("ns", "repo", "orig", 1)
	newUpstreamRevision := createEditPackageRevision(origRevision, 2)
	localRevision := createClonePackageRevision(origRevision, "clone", 1)
	localDraftRevision := createClonePackageRevision(origRevision, "clone-draft", 0)
	prs := []porchapi.PackageRevision{
		*origRevision,
		*newUpstreamRevision,
		*localRevision,
		*localDraftRevision,
	}

	// Create fake client and add the package revision to it
	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add porch API to scheme: %v", err)
	}
	interceptorFuncs := interceptor.Funcs{
		Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "PackageRevision" {
				obj.SetName("upgraded-pr")
			}
			return nil
		},
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if prList, ok := list.(*porchapi.PackageRevisionList); ok {
				// Return all package revisions and let client-side filtering handle it
				prList.Items = prs
				return nil
			}
			return nil
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(origRevision, newUpstreamRevision, localRevision, localDraftRevision).
		WithInterceptorFuncs(interceptorFuncs).
		Build()

	output := &bytes.Buffer{}
	commonRunner := createRunner(ctx, client, prs, "ns", 2)

	testCases := []struct {
		name           string
		args           []string
		expectedOutput string
		expectedError  string
		runner         *runner
	}{
		{
			name:           "Successful package upgrade",
			args:           []string{localRevision.Name},
			expectedOutput: fmt.Sprintf("%s upgraded to upgraded-pr\n", localRevision.Name),
			expectedError:  "",
			runner:         commonRunner,
		},
		{
			name:           "Successful package upgrade by finding latest",
			args:           []string{localRevision.Name},
			expectedOutput: fmt.Sprintf("%s upgraded to upgraded-pr\n", localRevision.Name),
			expectedError:  "",
			runner:         createRunner(ctx, client, prs, "ns", 0),
		},
		{
			name:           "Draft package revision",
			args:           []string{localDraftRevision.Name},
			expectedOutput: "",
			expectedError:  fmt.Sprintf("to upgrade a package, it must be in a published state, not %q", porchapi.PackageRevisionLifecycleDraft),
			runner:         commonRunner,
		},
		{
			name:           "Non-existent package revision",
			args:           []string{"non-existent-revision"},
			expectedOutput: "",
			expectedError:  "could not find package revision non-existent-revision",
			runner:         commonRunner,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.runner.Command.SetArgs(tc.args)
			tc.runner.Command.SetOut(output)

			output.Reset()
			err := tc.runner.runE(tc.runner.Command, tc.args)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("Expected error %q, got %v", tc.expectedError, err)
				}
			} else if err != nil {
				t.Fatalf("Unexpected error: %+v", err)
			}

			if diff := cmp.Diff(strings.TrimSpace(tc.expectedOutput), strings.TrimSpace(output.String())); diff != "" {
				t.Errorf("Unexpected output (-want, +got): %s", diff)
			}
		})
	}
}

func TestFindLatestPR(t *testing.T) {
	origRevision := createOrigPackageRevision("ns", "repo", "orig", 1)
	newUpstreamRevision := createEditPackageRevision(origRevision, 2)
	localRevision := createClonePackageRevision(origRevision, "clone", 1)
	prs := []porchapi.PackageRevision{
		*origRevision,
		*newUpstreamRevision,
		*localRevision,
	}

	// Create fake client with custom List interceptor
	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add porch API to scheme: %v", err)
	}
	interceptorFuncs := interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if prList, ok := list.(*porchapi.PackageRevisionList); ok {
				// Return all package revisions and let client-side filtering handle it
				prList.Items = prs
				return nil
			}
			return nil
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(origRevision, newUpstreamRevision, localRevision).
		WithInterceptorFuncs(interceptorFuncs).
		Build()

	r := createRunner(context.Background(), client, prs, "ns", 0)

	found := r.findLatestPackageRevisionForRef("orig", "repo")
	assert.Equal(t, "repo.orig.v2", found.Name)
	assert.Equal(t, 2, found.Spec.Revision)
}

func TestFindEditOrigin(t *testing.T) {
	const ns = "ns"
	downstreamv1 := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "downstream.v1",
			Namespace: ns,
		},
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						Upstream: porchapi.UpstreamPackage{
							UpstreamRef: &porchapi.PackageRevisionRef{
								Name: "upstream.v1",
							},
						},
					},
				},
			},
		},
	}
	downstreamv2 := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "downstream.v2",
			Namespace: ns,
		},
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeEdit,
					Edit: &porchapi.PackageEditTaskSpec{
						Source: &porchapi.PackageRevisionRef{
							Name: "downstream.v1",
						},
					},
				},
			},
		},
	}
	downstreamv3 := *downstreamv2.DeepCopy()
	downstreamv3.Name = "downstream.v3"
	downstreamv3.Spec.Tasks[0].Edit.Source = &porchapi.PackageRevisionRef{Name: "downstream.v2"}

	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add porch API to scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&downstreamv1, &downstreamv2, &downstreamv3).
		Build()

	// empty prs to force GET-based traversal in findEditOrigin
	r := createRunner(context.Background(), c, []porchapi.PackageRevision{}, ns, 0)

	found := r.findUpstreamName(&downstreamv3)
	assert.Equal(t, "upstream.v1", found)
}

func TestDiscoverUpdates(t *testing.T) {
	const ns = "ns"

	ctx := context.Background()

	basePr := createOrigPackageRevision("ns", "repo", "orig", 1)
	basePrV2 := createEditPackageRevision(basePr, 2)

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().
		List(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).
		Run(func(_ context.Context, list client.ObjectList, opts ...client.ListOption) {
			list.(*configapi.RepositoryList).Items = make([]configapi.Repository, 1)
			list.(*configapi.RepositoryList).Items[0] = configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "repo",
					Namespace: ns,
				},
				Spec: configapi.RepositorySpec{
					Description: "Sample Git repository",
					Deployment:  false,
					Type:        "git",
					Git: &configapi.GitRepository{
						Repo:   "https://github.com/user/repo",
						Branch: "/main",
						Author: "James Bond",
						Email:  "jamesbond@007.com",
					},
				},
			}
		})
	output := &bytes.Buffer{}

	t.Run("pr does not exist", func(t *testing.T) {
		r := createRunnerWithDiscovery(ctx, mockClient, []porchapi.PackageRevision{}, ns, "upstream", 0)
		r.Command.SetOut(output)

		err := r.discoverUpdates(r.Command, []string{basePr.Name})
		assert.ErrorContains(t, err, "could not find")
	})

	t.Run("all downstream up to date", func(t *testing.T) {
		r := createRunnerWithDiscovery(ctx, mockClient, []porchapi.PackageRevision{*basePr}, ns, "downstream", 0)
		r.Command.SetOut(output)

		err := r.discoverUpdates(r.Command, []string{})
		assert.NoError(t, err)
		assert.Contains(t, output.String(), "All downstream packages are up to date.")
	})

	output.Reset()

	t.Run("downstream update found", func(t *testing.T) {
		clonedPr := createClonePackageRevision(basePr, "clone", 1)
		r := createRunnerWithDiscovery(ctx, mockClient, []porchapi.PackageRevision{*basePr, *basePrV2, *clonedPr}, ns, "downstream", 0)
		r.Command.SetOut(output)
		err := r.discoverUpdates(r.Command, []string{basePrV2.Name})
		assert.NoError(t, err)
		assert.Regexp(t, regexp.MustCompile(`repo\.orig\.v2\s+repo.orig.v1\s+v1->v2`), output.String())
	})

	output.Reset()

	t.Run("all upstreams up to date", func(t *testing.T) {
		r := createRunnerWithDiscovery(ctx, mockClient, []porchapi.PackageRevision{*basePr}, ns, "upstream", 0)
		r.Command.SetOut(output)
		err := r.discoverUpdates(r.Command, []string{basePr.Name})
		assert.NoError(t, err)
		assert.Regexp(t, regexp.MustCompile(`repo\.orig\.v1\s+repo\s+No update available`), output.String())
	})

	output.Reset()

	t.Run("upstream update found", func(t *testing.T) {
		r := createRunnerWithDiscovery(ctx, mockClient, []porchapi.PackageRevision{*basePr, *basePrV2}, ns, "upstream", 0)
		r.Command.SetOut(output)
		err := r.discoverUpdates(r.Command, []string{basePr.Name})
		assert.NoError(t, err)
		assert.Regexp(t, regexp.MustCompile(`repo\.orig\.v1\s+repo\s+v2`), output.String())
	})

	output.Reset()

	t.Run("invalid discovery param", func(t *testing.T) {
		r := createRunnerWithDiscovery(ctx, mockClient, []porchapi.PackageRevision{*basePr, *basePrV2}, ns, "lowstream", 0)
		r.Command.SetOut(output)
		err := r.discoverUpdates(r.Command, []string{basePr.Name})
		assert.ErrorContains(t, err, "invalid argument \"lowstream\" for --discover")
	})
}

func TestPreRunStrategyValidation(t *testing.T) {
	ns := "ns"
	dummyApiServer := "http://localhost:9999"
	cfg := &genericclioptions.ConfigFlags{
		APIServer: &dummyApiServer,
		Namespace: &ns,
	}
	ctx := context.Background()

	testCases := []struct {
		name                 string
		strategy             string
		discover             string
		validationShouldPass bool
		expectedErrorMsg     string
	}{
		{
			name:                 "Valid strategy: copy-merge",
			strategy:             string(porchapi.CopyMerge),
			validationShouldPass: true,
		},
		{
			name:                 "Empty strategy is valid (uses default resource-merge)",
			strategy:             "",
			validationShouldPass: true,
		},
		{
			name:                 "Invalid strategy",
			strategy:             "non-existent-strategy",
			validationShouldPass: false,
			expectedErrorMsg:     "invalid strategy \"non-existent-strategy\"; must be one of:",
		},
		{
			name:                 "Valid strategy with discover mode fails connection",
			strategy:             string(porchapi.ResourceMerge),
			discover:             "upstream",
			validationShouldPass: true,
			expectedErrorMsg:     "connection refused",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{
				ctx:       ctx,
				cfg:       cfg,
				revision:  2,
				workspace: "v2",
				strategy:  tc.strategy,
				discover:  tc.discover,
			}
			r.Command = NewCommand(r.ctx, r.cfg)

			err := r.preRunE(r.Command, []string{"some-package-revision"})

			if !tc.validationShouldPass {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else if tc.discover != "" {
				// For valid strategies in discover mode, validation passes but connection fails
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				// For valid strategies in non-discover mode, preRunE should succeed
				assert.NoError(t, err)
			}
		})
	}
}

func TestFindUpstreamByLock(t *testing.T) {
	const ns = "ns"

	testCases := []struct {
		name     string
		lock     *porchapi.Locator
		prs      []porchapi.PackageRevision
		expected string
	}{
		{
			name:     "nil lock returns nil",
			lock:     nil,
			prs:      []porchapi.PackageRevision{},
			expected: "",
		},
		{
			name: "lock with nil Git returns nil",
			lock: &porchapi.Locator{
				Git: nil,
			},
			prs:      []porchapi.PackageRevision{},
			expected: "",
		},
		{
			name: "no matching package revisions",
			lock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v1",
				},
			},
			prs: []porchapi.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "different-repo"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  1,
						Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: &porchapi.Locator{
							Git: &porchapi.GitLock{
								Repo:      "https://github.com/different/repo",
								Directory: "packages/foo",
								Ref:       "refs/tags/v1",
							},
						},
					},
				},
			},
			expected: "",
		},
		{
			name: "finds exact match with same ref",
			lock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v1",
				},
			},
			prs: []porchapi.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "matching-pr-v1"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  1,
						Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: &porchapi.Locator{
							Git: &porchapi.GitLock{
								Repo:      "https://github.com/user/repo",
								Directory: "packages/foo",
								Ref:       "refs/tags/v1",
							},
						},
					},
				},
			},
			expected: "matching-pr-v1",
		},
		{
			name: "finds highest revision when multiple matches with same ref",
			lock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v2",
				},
			},
			prs: []porchapi.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "matching-pr-v1"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  1,
						Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: &porchapi.Locator{
							Git: &porchapi.GitLock{
								Repo:      "https://github.com/user/repo",
								Directory: "packages/foo",
								Ref:       "refs/tags/v2",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "matching-pr-v3"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  3,
						Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: &porchapi.Locator{
							Git: &porchapi.GitLock{
								Repo:      "https://github.com/user/repo",
								Directory: "packages/foo",
								Ref:       "refs/tags/v2",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "matching-pr-v2"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  2,
						Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: &porchapi.Locator{
							Git: &porchapi.GitLock{
								Repo:      "https://github.com/user/repo",
								Directory: "packages/foo",
								Ref:       "refs/tags/v2",
							},
						},
					},
				},
			},
			expected: "matching-pr-v3",
		},
		{
			name: "ignores draft package revisions",
			lock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v1",
				},
			},
			prs: []porchapi.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "draft-pr"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  1,
						Lifecycle: porchapi.PackageRevisionLifecycleDraft,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: &porchapi.Locator{
							Git: &porchapi.GitLock{
								Repo:      "https://github.com/user/repo",
								Directory: "packages/foo",
								Ref:       "refs/tags/v1",
							},
						},
					},
				},
			},
			expected: "",
		},
		{
			name: "ignores PRs with no upstream lock",
			lock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v1",
				},
			},
			prs: []porchapi.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "no-lock-pr"},
					Spec: porchapi.PackageRevisionSpec{
						Revision:  1,
						Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					},
					Status: porchapi.PackageRevisionStatus{
						UpstreamLock: nil,
					},
				},
			},
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := createRunner(context.Background(), fake.NewClientBuilder().Build(), tc.prs, ns, 0)

			result := r.findUpstreamByLock(tc.lock)

			if tc.expected == "" {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tc.expected, result.Name)
			}
		})
	}
}

func TestMatchesTarget(t *testing.T) {
	testCases := []struct {
		name      string
		candidate porchapi.PackageRevision
		target    *porchapi.GitLock
		expected  bool
	}{
		{
			name: "draft package revision does not match",
			candidate: porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
				Status: porchapi.PackageRevisionStatus{
					UpstreamLock: &porchapi.Locator{
						Git: &porchapi.GitLock{
							Repo:      "https://github.com/user/repo",
							Directory: "packages/foo",
						},
					},
				},
			},
			target: &porchapi.GitLock{
				Repo:      "https://github.com/user/repo",
				Directory: "packages/foo",
			},
			expected: false,
		},
		{
			name: "candidate with no upstream lock does not match",
			candidate: porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
				Status: porchapi.PackageRevisionStatus{
					UpstreamLock: nil,
				},
			},
			target: &porchapi.GitLock{
				Repo:      "https://github.com/user/repo",
				Directory: "packages/foo",
			},
			expected: false,
		},
		{
			name: "candidate with no git lock does not match",
			candidate: porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
				Status: porchapi.PackageRevisionStatus{
					UpstreamLock: &porchapi.Locator{
						Git: nil,
					},
				},
			},
			target: &porchapi.GitLock{
				Repo:      "https://github.com/user/repo",
				Directory: "packages/foo",
			},
			expected: false,
		},
		{
			name: "different repo does not match",
			candidate: porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
				Status: porchapi.PackageRevisionStatus{
					UpstreamLock: &porchapi.Locator{
						Git: &porchapi.GitLock{
							Repo:      "https://github.com/different/repo",
							Directory: "packages/foo",
						},
					},
				},
			},
			target: &porchapi.GitLock{
				Repo:      "https://github.com/user/repo",
				Directory: "packages/foo",
			},
			expected: false,
		},
		{
			name: "different directory does not match",
			candidate: porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
				Status: porchapi.PackageRevisionStatus{
					UpstreamLock: &porchapi.Locator{
						Git: &porchapi.GitLock{
							Repo:      "https://github.com/user/repo",
							Directory: "packages/bar",
						},
					},
				},
			},
			target: &porchapi.GitLock{
				Repo:      "https://github.com/user/repo",
				Directory: "packages/foo",
			},
			expected: false,
		},
		{
			name: "exact match returns true",
			candidate: porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
				Status: porchapi.PackageRevisionStatus{
					UpstreamLock: &porchapi.Locator{
						Git: &porchapi.GitLock{
							Repo:      "https://github.com/user/repo",
							Directory: "packages/foo",
							Ref:       "refs/tags/v1",
						},
					},
				},
			},
			target: &porchapi.GitLock{
				Repo:      "https://github.com/user/repo",
				Directory: "packages/foo",
				Ref:       "refs/tags/v2",
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{}
			result := r.matchesTarget(tc.candidate, tc.target)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestFindUpstreamInEditTaskWithUpstreamLock(t *testing.T) {
	const ns = "ns"

	editPr := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "broken-edit-pr",
			Namespace: ns,
		},
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeEdit,
					Edit: &porchapi.PackageEditTaskSpec{
						Source: &porchapi.PackageRevisionRef{
							Name: "non-existent-source",
						},
					},
				},
			},
		},
		Status: porchapi.PackageRevisionStatus{
			UpstreamLock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v1",
				},
			},
		},
	}

	upstreamPr := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upstream-pr-v2",
			Namespace: ns,
		},
		Spec: porchapi.PackageRevisionSpec{
			Revision:  2,
			Lifecycle: porchapi.PackageRevisionLifecyclePublished,
		},
		Status: porchapi.PackageRevisionStatus{
			UpstreamLock: &porchapi.Locator{
				Git: &porchapi.GitLock{
					Repo:      "https://github.com/user/repo",
					Directory: "packages/foo",
					Ref:       "refs/tags/v1",
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add porch API to scheme: %v", err)
	}
	interceptorFuncs := interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if prList, ok := list.(*porchapi.PackageRevisionList); ok {
				prList.Items = []porchapi.PackageRevision{editPr, upstreamPr}
			}
			return nil
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&editPr, &upstreamPr).
		WithInterceptorFuncs(interceptorFuncs).
		Build()

	// empty prs to force listPackageRevisions call
	r := createRunner(context.Background(), c, []porchapi.PackageRevision{}, ns, 0)

	result := r.findUpstreamName(&editPr)

	assert.Equal(t, "upstream-pr-v2", result)
}

func TestFindUpstreamInEditTaskNoUpstreamLock(t *testing.T) {
	const ns = "ns"

	// edit package revision with no upstream lock
	editPrNoLock := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: "edit-pr-no-lock",
		},
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeEdit,
					Edit: &porchapi.PackageEditTaskSpec{
						Source: &porchapi.PackageRevisionRef{
							Name: "non-existent-source",
						},
					},
				},
			},
		},
		Status: porchapi.PackageRevisionStatus{
			UpstreamLock: nil,
		},
	}

	prs := []porchapi.PackageRevision{editPrNoLock}
	r := createRunner(context.Background(), fake.NewClientBuilder().Build(), prs, ns, 0)

	result := r.findUpstreamName(&editPrNoLock)

	assert.Equal(t, "", result)
}

func TestFindUpstreamInUpgradeTask(t *testing.T) {
	const ns = "ns"

	upgradePr := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: "upgrade-pr",
		},
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeUpgrade,
					Upgrade: &porchapi.PackageUpgradeTaskSpec{
						NewUpstream: porchapi.PackageRevisionRef{
							Name: "new-upstream-v2",
						},
					},
				},
			},
		},
	}

	prs := []porchapi.PackageRevision{upgradePr}
	r := createRunner(context.Background(), fake.NewClientBuilder().Build(), prs, ns, 0)

	result := r.findUpstreamName(&upgradePr)

	assert.Equal(t, "new-upstream-v2", result)
}

// TestLastErrWorkaround verifies the lastErr workaround catches errors
// when retry.RetryOnConflict returns err=nil despite the inner function
// returning an error, due to a bug. This can be triggered when infrastructure
// issues happen. The easiest way to trigger this in tests is to use an
// unreachable cluster.
func TestLastErrWorkaround(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}
	c, err := client.New(&rest.Config{Host: "https://127.0.0.1:1", Timeout: 1, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("error creating client: %v", err)
	}
	pr := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pkg", Namespace: "ns"},
	}
	ns := "ns"
	r := &runner{
		ctx:     context.Background(),
		cfg:     &genericclioptions.ConfigFlags{Namespace: &ns},
		client:  c,
		Command: NewCommand(context.Background(), &genericclioptions.ConfigFlags{Namespace: &ns}),
		prs:     []porchapi.PackageRevision{pr},
	}
	err = r.runE(r.Command, []string{"test-pkg"})
	if err == nil {
		t.Fatal("expected error but got nil")
	}
}

func TestAvailableUpdatesWithDirectory(t *testing.T) {
	const ns = "ns"
	ctx := context.Background()

	// Create upstream package revisions in a repo with directory configured
	upstreamPkg1 := createOrigPackageRevision(ns, "blueprints", "pkg", 1)
	upstreamPkg2 := createEditPackageRevision(upstreamPkg1, 2)
	upstreamPkg3 := createEditPackageRevision(upstreamPkg1, 3)

	// Create downstream package that references upstream with directory in upstreamLock
	downstreamPkg := createClonePackageRevision(upstreamPkg1, "downstream-pkg", 1)
	downstreamPkg.Status.UpstreamLock = &porchapi.Locator{
		Git: &porchapi.GitLock{
			Repo: "https://github.com/user/repo.git",
			Ref:  "packages/pkg/v1", // includes directory prefix
		},
	}

	prs := []porchapi.PackageRevision{*upstreamPkg1, *upstreamPkg2, *upstreamPkg3, *downstreamPkg}

	// Create repository with directory configured
	repoWithDir := configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "blueprints",
			Namespace: ns,
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:      "https://github.com/user/repo.git",
				Directory: "/packages",
			},
		},
	}

	repoList := &configapi.RepositoryList{
		Items: []configapi.Repository{repoWithDir},
	}

	r := createRunner(ctx, fake.NewClientBuilder().Build(), prs, ns, 0)

	// Test that availableUpdates finds v2 and v3 when downstream is at v1
	availableUpdates, upstreamName, draftName, err := r.availableUpdates(downstreamPkg.Status.UpstreamLock, repoList)

	assert.NoError(t, err)
	assert.Equal(t, "blueprints", upstreamName)
	assert.Equal(t, "", draftName)
	assert.Len(t, availableUpdates, 2, "Should find v2 and v3 as available updates")
	assert.Equal(t, 2, availableUpdates[0].Spec.Revision)
	assert.Equal(t, 3, availableUpdates[1].Spec.Revision)
}
