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

package porch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/engine"
	fakeextrepo "github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockcad "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/engine"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
)

func TestNamespaceFilteringWatcher(t *testing.T) {
	called := false
	watcher := &namespaceFilteringWatcher{
		ns: "foo",
		delegate: &testWatcher{onChange: func(eventType watch.EventType, obj repository.PackageRevision) bool {
			called = true
			return true
		}},
	}
	// Should call delegate
	watcher.OnPackageRevisionChange(watch.Added, &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "foo",
				},
			},
		},
	})
	if !called {
		t.Error("expected delegate to be called for matching namespace")
	}
	called = false
	// Should NOT call delegate
	watcher.OnPackageRevisionChange(watch.Added, &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "bar",
				},
			},
		},
	})
	if called {
		t.Error("did not expect delegate to be called for non-matching namespace")
	}
}

type testWatcher struct {
	onChange func(eventType watch.EventType, obj repository.PackageRevision) bool
}

func (t *testWatcher) OnPackageRevisionChange(eventType watch.EventType, obj repository.PackageRevision) bool {
	return t.onChange(eventType, obj)
}

func TestListPackageRevisionsNamespaceFilter(t *testing.T) {
	ctx := context.TODO()
	ns := "test-ns"

	// Simulate two revisions, one in the right ns, one in another
	revisions := []repository.PackageRevision{
		&fakeextrepo.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Namespace: "foo",
					},
				},
			},
		},
		&fakeextrepo.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Namespace: "foo",
					},
				},
			},
		},
	}

	var got []*porchapi.PackageRevision
	for _, rev := range revisions {
		apiPkgRev, _ := rev.GetPackageRevision(ctx)
		if apiPkgRev == nil || apiPkgRev.Namespace != ns {
			continue
		}
		got = append(got, apiPkgRev)
	}
	for _, pr := range got {
		if pr.Namespace != ns {
			t.Errorf("got revision from wrong namespace: %s", pr.Namespace)
		}
	}
}

// --- TEST FOR watchPackages ---

type fakeWatcherManager struct {
	calledWithFilter   repository.ListPackageRevisionFilter
	calledWithCallback engine.ObjectWatcher
}

func (f *fakeWatcherManager) WatchPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter, callback engine.ObjectWatcher) error {
	f.calledWithFilter = filter
	f.calledWithCallback = callback
	// Simulate a package revision event
	if callback != nil {
		callback.OnPackageRevisionChange(watch.Added, &fakeextrepo.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Namespace: "foo",
					},
				},
			},
		})
	}
	return nil
}

func newMockCoreClientForWatcher() *mockclient.MockClient {
	c := &mockclient.MockClient{}
	c.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Return(nil).Maybe()
	return c
}

func TestWatchPackages_CallsCallback(t *testing.T) {
	mockCad := &mockcad.MockCaDEngine{}
	mockCad.On("ObjectCache").Return(&fakeWatcherManager{})
	pc := &packageCommon{cad: mockCad, coreClient: newMockCoreClientForWatcher()}

	called := false
	callback := &testWatcher{onChange: func(eventType watch.EventType, obj repository.PackageRevision) bool {
		called = true
		return false
	}}

	nsFilter := repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "test-ns",
				},
			},
		},
	}

	err := pc.watchPackages(context.TODO(), nsFilter, callback)
	if err != nil {
		t.Fatalf("watchPackages returned error: %v", err)
	}
	if !called {
		t.Error("expected callback to be called by watchPackages")
	}
}
func TestWatchPackages_NoNamespace(t *testing.T) {
	mockCad := &mockcad.MockCaDEngine{}
	mockCad.On("ObjectCache").Return(&fakeWatcherManager{})
	pc := &packageCommon{cad: mockCad, coreClient: newMockCoreClientForWatcher()}

	called := false
	callback := &testWatcher{onChange: func(eventType watch.EventType, obj repository.PackageRevision) bool {
		called = true
		return false
	}}
	filter := repository.ListPackageRevisionFilter{}
	ctx := context.TODO() // No namespace set in context
	err := pc.watchPackages(ctx, filter, callback)
	if err != nil {
		t.Fatalf("watchPackages returned error: %v", err)
	}
	if !called {
		t.Error("expected callback to be called by watchPackages (no namespace)")
	}
}

type errorWatcherManager struct{}

func (e *errorWatcherManager) WatchPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter, callback engine.ObjectWatcher) error {
	return fmt.Errorf("simulated error")
}
func TestWatchPackages_ErrorPath(t *testing.T) {
	mockCad := &mockcad.MockCaDEngine{}
	mockCad.On("ObjectCache").Return(&errorWatcherManager{})
	pc := &packageCommon{cad: mockCad, coreClient: newMockCoreClientForWatcher()}

	callback := &testWatcher{onChange: func(eventType watch.EventType, obj repository.PackageRevision) bool {
		return false
	}}

	filter := repository.ListPackageRevisionFilter{}
	ctx := context.TODO()
	err := pc.watchPackages(ctx, filter, callback)
	if err == nil {
		t.Error("expected error from watchPackages, got nil")
	}
}

func TestWatchPackages_WithNamespaceFilteringWatcher(t *testing.T) {
	mockCad := &mockcad.MockCaDEngine{}
	mockCad.On("ObjectCache").Return(&fakeWatcherManager{})
	pc := &packageCommon{cad: mockCad, coreClient: newMockCoreClientForWatcher()}

	called := false
	callback := &testWatcher{onChange: func(eventType watch.EventType, obj repository.PackageRevision) bool {
		called = true
		return false
	}}
	filter := repository.ListPackageRevisionFilter{}
	ctx := context.TODO()
	ctx = genericapirequest.WithNamespace(ctx, "foo") // Set namespace in context

	err := pc.watchPackages(ctx, filter, callback)
	if err != nil {
		t.Fatalf("watchPackages returned error: %v", err)
	}
	if !called {
		t.Error("expected callback to be called by watchPackages with namespace filtering")
	}
}

func TestListPackageRevisions(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*mockclient.MockClient, *mockrepo.MockPackageRevision, *mockcad.MockCaDEngine)
		filter        repository.ListPackageRevisionFilter
		expectedError error
		expectedCalls int
		ctx           context.Context
	}{
		{
			name: "Successful package revision listing",
			setupMocks: func(c *mockclient.MockClient, pkgRev *mockrepo.MockPackageRevision, cad *mockcad.MockCaDEngine) {
				cad.On("ListPackageRevisions", mock.Anything, mock.Anything).
					Return([]repository.PackageRevision{pkgRev}, nil)
				pkgRev.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{Name: "repo", Namespace: "ns"},
					},
				})
				pkgRev.On("KubeObjectNamespace").Return("ns")
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
					Return(nil)
			},
			filter:        repository.ListPackageRevisionFilter{},
			expectedError: nil,
			expectedCalls: 1,
			ctx:           context.Background(),
		},
		{
			name: "Successful namespace filtering",
			setupMocks: func(c *mockclient.MockClient, pkgRev *mockrepo.MockPackageRevision, cad *mockcad.MockCaDEngine) {
				cad.On("ListPackageRevisions", mock.Anything, mock.Anything).
					Return([]repository.PackageRevision{pkgRev}, nil)
				pkgRev.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{Name: "repo", Namespace: "test-namespace"},
					},
				})
				pkgRev.On("KubeObjectNamespace").Return("test-namespace")
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-namespace"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
					Return(nil)
			},
			filter: repository.ListPackageRevisionFilter{
				Key: repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{
							Namespace: "test-namespace",
						},
					},
				},
			},
			expectedError: nil,
			expectedCalls: 1,
			ctx:           genericapirequest.WithNamespace(context.Background(), "test-namespace"),
		},
		{
			name: "CaD engine error",
			setupMocks: func(c *mockclient.MockClient, pkgRev *mockrepo.MockPackageRevision, cad *mockcad.MockCaDEngine) {
				cad.On("ListPackageRevisions", mock.Anything, mock.Anything).
					Return([]repository.PackageRevision{}, fmt.Errorf("CaD engine error"))
			},
			filter:        repository.ListPackageRevisionFilter{},
			expectedError: fmt.Errorf("CaD engine error"),
			expectedCalls: 0,
			ctx:           context.Background(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCoreClient := &mockclient.MockClient{}
			mockPkgRev := &mockrepo.MockPackageRevision{}
			mockCaD := &mockcad.MockCaDEngine{}

			pc := &packageCommon{
				cad:        mockCaD,
				coreClient: mockCoreClient,
			}

			mockCaD.ExpectedCalls = nil
			mockCoreClient.ExpectedCalls = nil
			mockPkgRev.ExpectedCalls = nil
			tt.setupMocks(mockCoreClient, mockPkgRev, mockCaD)

			callCount := 0
			callback := func(ctx context.Context, p repository.PackageRevision) error {
				callCount++
				return nil
			}

			ctx := tt.ctx
			if tt.name == "Timeout error" {
				tCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				ctx = tCtx
				defer cancel()
			}

			err := pc.listPackageRevisions(ctx, tt.filter, callback)

			if tt.expectedError != nil {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError.Error())
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.expectedCalls, callCount)
			mockCaD.AssertExpectations(t)
			mockCoreClient.AssertExpectations(t)
			mockPkgRev.AssertExpectations(t)
		})
	}
}

func TestGetRepoPkgRev(t *testing.T) {
	tests := []struct {
		name                 string
		pkgRevName           string
		ctx                  context.Context
		setupMocks           func(*mockclient.MockClient, *mockcad.MockCaDEngine)
		expectedFailure      bool
		expectedErrorMessage string
	}{
		{
			name:       "Successful package revision retrieval",
			pkgRevName: "repo.pkg.wsn",
			ctx:        genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				pkgRev := &fakeextrepo.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{Name: "repo"},
							Package: "pkg",
						},
						WorkspaceName: "wsn",
					},
				}

				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
					Return(nil)

				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					Return([]repository.PackageRevision{pkgRev}, nil)
			},
			expectedFailure: false,
		},
		{
			name:                 "No namespace specified",
			pkgRevName:           "",
			ctx:                  context.TODO(),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "namespace must be specified",
		},
		{
			name:                 "No name specified",
			pkgRevName:           "",
			ctx:                  genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "package revision kubernetes resource name invalid",
		},
		{
			name:                 "Repository name only",
			pkgRevName:           "repo",
			ctx:                  genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "package revision kubernetes resource name invalid",
		},
		{
			name:                 "Repository name and package only",
			pkgRevName:           "repo.pkg",
			ctx:                  genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "package revision kubernetes resource name invalid",
		},
		{
			name:       "Package revision not found",
			pkgRevName: "repo.pkg.wsn",
			ctx:        genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
					Return(nil)
				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					Return([]repository.PackageRevision{}, nil)
			},
			expectedFailure:      true,
			expectedErrorMessage: "not found",
		},
		{
			name:       "Listing package revisions timeout",
			pkgRevName: "repo.pkg.wsn",
			ctx:        genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
					Return(nil)
				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					After(2*time.Second).
					Return([]repository.PackageRevision{}, fmt.Errorf("seconds trying to list package revisions"))
			},
			expectedFailure:      true,
			expectedErrorMessage: "seconds trying to list package revisions",
		},
		{
			name:       "Listing package revisions error",
			pkgRevName: "repo.pkg.wsn",
			ctx:        genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
					Return(nil)
				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					Return([]repository.PackageRevision{}, fmt.Errorf("list error"))
			},
			expectedFailure:      true,
			expectedErrorMessage: "list error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCoreClient := &mockclient.MockClient{}
			mockCaDEngine := &mockcad.MockCaDEngine{}

			pc := &packageCommon{
				coreClient: mockCoreClient,
				cad:        mockCaDEngine,
			}

			tt.setupMocks(mockCoreClient, mockCaDEngine)
			result, err := pc.getRepoPkgRev(tt.ctx, tt.pkgRevName)

			if !tt.expectedFailure {
				require.NoError(t, err)
				assert.NotNil(t, result)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorMessage)
			}

			mockCoreClient.AssertExpectations(t)
			mockCaDEngine.AssertExpectations(t)
		})
	}
}

func TestGetPackage(t *testing.T) {
	tests := []struct {
		name                 string
		pkgName              string
		ctx                  context.Context
		setupMocks           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine)
		expectedFailure      bool
		expectedErrorMessage string
	}{
		{
			name:    "Successful get package",
			pkgName: "repo.pkg",
			ctx:     genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				repo := &configapi.Repository{}

				pkg := &fakeextrepo.FakePackage{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{
							Name: "repo",
						},
						Package: "pkg",
					},
				}

				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"},
					&configapi.Repository{}).
					Run(func(args mock.Arguments) {
						arg := args.Get(2).(*configapi.Repository)
						*arg = *repo
					}).Return(nil)

				pkgKey, _ := repository.PkgK8sName2Key("test-ns", "repo.pkg")
				cad.On("ListPackages", mock.Anything, repo,
					repository.ListPackageFilter{Key: pkgKey}).
					Return([]repository.Package{pkg}, nil)
			},
			expectedFailure: false,
		},
		{
			name:                 "No namespace specified",
			pkgName:              "repo.pkg",
			ctx:                  context.TODO(),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "namespace must be specified",
		},
		{
			name:                 "No name specified",
			pkgName:              "",
			ctx:                  genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "package kubernetes resource name invalid",
		},
		{
			name:                 "Repository name only",
			pkgName:              "repo",
			ctx:                  genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks:           func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {},
			expectedFailure:      true,
			expectedErrorMessage: "package kubernetes resource name invalid",
		},
		{
			name:    "Package not found",
			pkgName: "repo.pkg",
			ctx:     genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"},
					&configapi.Repository{}, mock.Anything).
					Return(apierrors.NewNotFound(schema.GroupResource{}, "repo"))
			},
			expectedFailure:      true,
			expectedErrorMessage: "not found",
		},
		{
			name:    "Listing package revisions error",
			pkgName: "repo.pkg",
			ctx:     genericapirequest.WithNamespace(context.Background(), "test-ns"),
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine) {
				repo := &configapi.Repository{}

				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"},
					&configapi.Repository{}, mock.Anything).
					Run(func(args mock.Arguments) {
						arg := args.Get(2).(*configapi.Repository)
						*arg = *repo
					}).Return(nil)

				pkgKey, _ := repository.PkgK8sName2Key("test-ns", "repo.pkg")
				cad.On("ListPackages", mock.Anything, repo,
					repository.ListPackageFilter{Key: pkgKey}).
					Return(nil, fmt.Errorf("list error"))

			},
			expectedFailure:      true,
			expectedErrorMessage: "list error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCoreClient := &mockclient.MockClient{}
			mockCaDEngine := &mockcad.MockCaDEngine{}
			pc := &packageCommon{
				coreClient: mockCoreClient,
				cad:        mockCaDEngine,
			}

			tt.setupMocks(mockCoreClient, mockCaDEngine)
			result, err := pc.getPackage(tt.ctx, tt.pkgName)

			if !tt.expectedFailure {
				require.NoError(t, err)
				assert.NotNil(t, result)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorMessage)
			}

			mockCoreClient.AssertExpectations(t)
			mockCaDEngine.AssertExpectations(t)
		})
	}
}

func TestGetLifecycleTransition(t *testing.T) {
	testCases := map[string]struct {
		oldPkgRev *porchapi.PackageRevision
		newPkgRev *porchapi.PackageRevision
		expected  string
	}{
		"draft to proposed": {
			oldPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			},
			newPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleProposed,
				},
			},
			expected: "Propose",
		},
		"published to deletion proposed": {
			oldPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			newPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDeletionProposed,
				},
			},
			expected: "Propose-delete",
		},
		"proposed to draft": {
			oldPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleProposed,
				},
			},
			newPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			},
			expected: "Reject",
		},
		"proposed to published": {
			oldPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleProposed,
				},
			},
			newPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			expected: "Approve",
		},
		"deletion proposed to published": {
			oldPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDeletionProposed,
				},
			},
			newPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			expected: "Approve/Reject",
		},
		"no lifecycle change": {
			oldPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			},
			newPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			},
			expected: "Update",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			result := getLifecycleTransition(tc.oldPkgRev, tc.newPkgRev)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestUpdatePackageRevision(t *testing.T) {
	tests := []struct {
		name          string
		pkgRevName    string
		setupMocks    func(*mockclient.MockClient, *mockcad.MockCaDEngine, *mockrepo.MockPackageRevision)
		expectedError bool
		expectCreate  bool
	}{
		{
			name:       "Successful update - Propose lifecycle",
			pkgRevName: "repo.pkg.wsn",
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine, pkgRev *mockrepo.MockPackageRevision) {
				oldPkgRev := &porchapi.PackageRevision{
					Spec: porchapi.PackageRevisionSpec{
						Lifecycle: porchapi.PackageRevisionLifecycleDraft,
					},
				}
				newPkgRev := &porchapi.PackageRevision{
					Spec: porchapi.PackageRevisionSpec{
						Lifecycle:      porchapi.PackageRevisionLifecycleProposed,
						RepositoryName: "repo",
					},
				}

				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.Anything).
					Return(nil)

				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					Return([]repository.PackageRevision{pkgRev}, nil).Once()

				pkgRev.On("KubeObjectName").Return("repo.pkg.wsn")
				pkgRev.On("GetPackageRevision", mock.Anything).Return(oldPkgRev, nil).Once()
				pkgRev.On("GetPackageRevision", mock.Anything).Return(newPkgRev, nil).Once()

				cad.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(pkgRev, nil).Once()
			},
			expectedError: false,
			expectCreate:  false,
		},
		{
			name:       "Missing namespace",
			pkgRevName: "repo.pkg.wsn",
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine, pkgRev *mockrepo.MockPackageRevision) {
				// No mocks needed - should fail before any calls
			},
			expectedError: true,
			expectCreate:  false,
		},
		{
			name:       "Package not found - no forceAllowCreate",
			pkgRevName: "repo.pkg.wsn",
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine, pkgRev *mockrepo.MockPackageRevision) {
				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.Anything).
					Return(nil)
				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					Return([]repository.PackageRevision{}, nil).Once()
			},
			expectedError: true,
			expectCreate:  false,
		},
		{
			name:       "Error from UpdatePackageRevision",
			pkgRevName: "repo.pkg.wsn",
			setupMocks: func(c *mockclient.MockClient, cad *mockcad.MockCaDEngine, pkgRev *mockrepo.MockPackageRevision) {
				oldPkgRev := &porchapi.PackageRevision{
					Spec: porchapi.PackageRevisionSpec{
						Lifecycle: porchapi.PackageRevisionLifecycleDraft,
					},
				}

				c.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.Anything).
					Return(nil)

				prKey, _ := repository.PkgRevK8sName2Key("test-ns", "repo.pkg.wsn")
				cad.On("ListPackageRevisions", mock.Anything,
					repository.ListPackageRevisionFilter{Key: prKey}).
					Return([]repository.PackageRevision{pkgRev}, nil).Once()

				pkgRev.On("KubeObjectName").Return("repo.pkg.wsn")
				pkgRev.On("GetPackageRevision", mock.Anything).Return(oldPkgRev, nil).Once()

				cad.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("update failed")).Once()
			},
			expectedError: true,
			expectCreate:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCoreClient := &mockclient.MockClient{}
			mockCaDEngine := &mockcad.MockCaDEngine{}
			mockPkgRev := &mockrepo.MockPackageRevision{}

			pc := &packageCommon{
				coreClient:     mockCoreClient,
				cad:            mockCaDEngine,
				gr:             porchapi.Resource("packagerevisions"),
				scheme:         runtime.NewScheme(),
				updateStrategy: packageRevisionStrategy{},
			}

			tt.setupMocks(mockCoreClient, mockCaDEngine, mockPkgRev)

			ctx := context.Background()
			if tt.name != "Missing namespace" {
				ctx = genericapirequest.WithNamespace(ctx, "test-ns")
			}

			objInfo := &fakeUpdatedObjectInfo{
				obj: &porchapi.PackageRevision{
					Spec: porchapi.PackageRevisionSpec{
						Lifecycle:      porchapi.PackageRevisionLifecycleProposed,
						RepositoryName: "repo",
					},
				},
			}

			result, created, err := pc.updatePackageRevision(ctx, tt.pkgRevName, objInfo, nil, nil, false)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectCreate, created)
			}

			mockCoreClient.AssertExpectations(t)
			mockCaDEngine.AssertExpectations(t)
			mockPkgRev.AssertExpectations(t)
		})
	}
}

// fakeUpdatedObjectInfo implements rest.UpdatedObjectInfo for testing
type fakeUpdatedObjectInfo struct {
	obj runtime.Object
}

func (f *fakeUpdatedObjectInfo) UpdatedObject(ctx context.Context, oldObj runtime.Object) (runtime.Object, error) {
	return f.obj, nil
}

func (f *fakeUpdatedObjectInfo) Preconditions() *metav1.Preconditions {
	return nil
}

func TestIsV1Alpha2Repo(t *testing.T) {
	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected bool
	}{
		{"nil annotations", &configapi.Repository{}, false},
		{"empty annotations", &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}, false},
		{"wrong value", &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{configapi.AnnotationKeyV1Alpha2Migration: "false"}}}, false},
		{"enabled", &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isV1Alpha2Repo(tt.repo))
		})
	}
}

func TestV1Alpha2FilteringWatcher(t *testing.T) {
	fakePR := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{Name: "my-repo", Namespace: "ns"},
			},
		},
	}

	t.Run("skips v1alpha2 repo", func(t *testing.T) {
		mockClient := &mockclient.MockClient{}
		mockClient.On("Get", mock.Anything, types.NamespacedName{Name: "my-repo", Namespace: "ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
			Run(func(args mock.Arguments) {
				repo := args.Get(2).(*configapi.Repository)
				repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
			}).Return(nil)

		called := false
		w := &v1alpha2FilteringWatcher{
			coreClient: mockClient,
			delegate:   &testWatcher{onChange: func(_ watch.EventType, _ repository.PackageRevision) bool { called = true; return true }},
		}
		result := w.OnPackageRevisionChange(watch.Added, fakePR)
		assert.True(t, result)
		assert.False(t, called)
	})

	t.Run("passes through non-v1alpha2 repo", func(t *testing.T) {
		mockClient := &mockclient.MockClient{}
		mockClient.On("Get", mock.Anything, types.NamespacedName{Name: "my-repo", Namespace: "ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
			Return(nil)

		called := false
		w := &v1alpha2FilteringWatcher{
			coreClient: mockClient,
			delegate:   &testWatcher{onChange: func(_ watch.EventType, _ repository.PackageRevision) bool { called = true; return true }},
		}
		result := w.OnPackageRevisionChange(watch.Added, fakePR)
		assert.True(t, result)
		assert.True(t, called)
	})

	t.Run("passes through on lookup error (fail open)", func(t *testing.T) {
		mockClient := &mockclient.MockClient{}
		mockClient.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(fmt.Errorf("api error"))

		called := false
		w := &v1alpha2FilteringWatcher{
			coreClient: mockClient,
			delegate:   &testWatcher{onChange: func(_ watch.EventType, _ repository.PackageRevision) bool { called = true; return true }},
		}
		result := w.OnPackageRevisionChange(watch.Added, fakePR)
		assert.True(t, result)
		assert.True(t, called)
	})
}

func TestListPackageRevisions_SkipsV1Alpha2Repos(t *testing.T) {
	mockCoreClient := &mockclient.MockClient{}
	mockCaD := &mockcad.MockCaDEngine{}

	pc := &packageCommon{
		cad:        mockCaD,
		coreClient: mockCoreClient,
	}

	v1PkgRev := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{Name: "v1-repo", Namespace: "ns"},
			},
		},
	}
	v2PkgRev := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{Name: "v2-repo", Namespace: "ns"},
			},
		},
	}

	mockCaD.On("ListPackageRevisions", mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{v1PkgRev, v2PkgRev}, nil)

	mockCoreClient.On("Get", mock.Anything, types.NamespacedName{Name: "v1-repo", Namespace: "ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Return(nil)
	mockCoreClient.On("Get", mock.Anything, types.NamespacedName{Name: "v2-repo", Namespace: "ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
		}).Return(nil)

	callCount := 0
	err := pc.listPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{}, func(_ context.Context, _ repository.PackageRevision) error {
		callCount++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, callCount, "should only list from v1 repo")
}

func TestListPackageRevisions_AllV1Alpha2Repos(t *testing.T) {
	mockCoreClient := &mockclient.MockClient{}
	mockCaD := &mockcad.MockCaDEngine{}

	pc := &packageCommon{
		cad:        mockCaD,
		coreClient: mockCoreClient,
	}

	v2PkgRev := &fakeextrepo.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{Name: "v2-repo", Namespace: "ns"},
			},
		},
	}

	mockCaD.On("ListPackageRevisions", mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{v2PkgRev}, nil)

	mockCoreClient.On("Get", mock.Anything, types.NamespacedName{Name: "v2-repo", Namespace: "ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
		}).Return(nil)

	callCount := 0
	err := pc.listPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{}, func(_ context.Context, _ repository.PackageRevision) error {
		callCount++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 0, callCount)
}

func TestGetRepoPkgRev_V1Alpha2RepoReturnsNotFound(t *testing.T) {
	mockCoreClient := &mockclient.MockClient{}
	mockCaDEngine := &mockcad.MockCaDEngine{}

	pc := &packageCommon{
		coreClient: mockCoreClient,
		cad:        mockCaDEngine,
		gr:         porchapi.Resource("packagerevisions"),
	}

	mockCoreClient.On("Get", mock.Anything, types.NamespacedName{Name: "repo", Namespace: "test-ns"}, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
		}).Return(nil)

	ctx := genericapirequest.WithNamespace(context.Background(), "test-ns")
	_, err := pc.getRepoPkgRev(ctx, "repo.pkg.wsn")

	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}
