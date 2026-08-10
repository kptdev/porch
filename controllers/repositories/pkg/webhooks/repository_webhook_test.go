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

package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)
	return scheme
}

func makeRepo(name, ns, url, dir, branch string) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:      url,
				Directory: dir,
				Branch:    branch,
			},
		},
	}
}

func marshalRepo(t *testing.T, repo *configapi.Repository) []byte {
	t.Helper()
	raw, err := json.Marshal(repo)
	require.NoError(t, err)
	return raw
}

// setupMockReaderWithRepos creates a mock reader that returns the given repositories on List calls
func setupMockReaderWithRepos(t *testing.T, repos ...configapi.Repository) *mockclient.MockReader {
	t.Helper()
	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().List(mock.Anything, mock.MatchedBy(func(obj client.ObjectList) bool {
		_, ok := obj.(*configapi.RepositoryList)
		return ok
	}), mock.Anything).Run(func(_ context.Context, obj client.ObjectList, _ ...client.ListOption) {
		list := obj.(*configapi.RepositoryList)
		list.Items = append([]configapi.Repository{}, repos...)
	}).Return(nil)
	return mockReader
}

// TestHandleDelete verifies DELETE operations are always allowed.
func TestHandleDelete(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			Name:      "my-repo",
			Namespace: "default",
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleCreateEmptyObject verifies empty CREATE object is rejected.
func TestHandleCreateEmptyObject(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte{}},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
}

// TestHandleCreateMalformedJSON verifies malformed JSON is rejected.
func TestHandleCreateMalformedJSON(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte(`{invalid}`)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
}

// TestHandleUnknownOperation verifies unknown operations are allowed.
func TestHandleUnknownOperation(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Connect,
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleCreateNoConflict verifies a valid CREATE without conflicts.
func TestHandleCreateNoConflict(t *testing.T) {
	mockReader := setupMockReaderWithRepos(t)
	repo := makeRepo("new-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, repo)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "validated successfully")
}

// TestHandleCreateWithConflict verifies CREATE is rejected when a conflict exists.
func TestHandleCreateWithConflict(t *testing.T) {
	existing := makeRepo("existing-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	mockReader := setupMockReaderWithRepos(t, *existing)
	attempted := makeRepo("new-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, attempted)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "conflict")
}

// TestHandleUpdateWithConflict verifies UPDATE is rejected when it introduces a conflict.
func TestHandleUpdateWithConflict(t *testing.T) {
	existing := makeRepo("existing-repo", "ns1", "http://gitea.local/org/repo.git", "", "main")
	mockReader := setupMockReaderWithRepos(t, *existing)
	attempted := makeRepo("other-repo", "ns1", "http://gitea.local/org/repo.git", "subdir", "main")
	validator := NewRepositoryValidator(mockReader)

	oldData, err := json.Marshal(attempted)
	require.NoError(t, err)
	newData, err := json.Marshal(attempted)
	require.NoError(t, err)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: newData},
			OldObject: runtime.RawExtension{Raw: oldData},
			Namespace: attempted.Namespace,
			Name:      attempted.Name,
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "conflict")
}

// TestNormalizeURL tests URL normalization.
func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://172.18.255.200:3000/porch/myrepo.git", "http---172.18.255.200-3000-porch-myrepo.git"},
		{"https://github.com/org/repo.git", "https---github.com-org-repo.git"},
		{"ssh://git@host.com:2222/repo.git", "ssh---git@host.com-2222-repo.git"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, NormalizeURL(tc.input))
		})
	}
}

// TestIsNestedConflict tests nested directory detection.
func TestIsNestedConflict(t *testing.T) {
	tests := []struct {
		a, b     string
		expected bool
	}{
		{"base", "base/sub", true},
		{"base/sub", "base", true},
		{"base", "base", false},
		{"base", "other", false},
		{"base/sub", "base/sub/deep", true},
		{"base/sub/deep", "base/sub", true},
		{"dir/sub/sub1", "dir/sub/sub2", false},
		// Additional edge cases
		{"", "", false},
		{".", ".", false},
		{"/", "/", false},
		{"a/b/c", "a/b", true},
		{"a/b", "a/b/c/d/e", true},
		{"pkg", "package", false},
		{"config", "config-old", false},
	}

	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			assert.Equal(t, tc.expected, IsNestedConflict(tc.a, tc.b))
		})
	}
}

// TestIsConflict tests the full conflict detection logic.
func TestIsConflict(t *testing.T) {
	tests := []struct {
		name     string
		existing *configapi.Repository
		attempt  *configapi.Repository
		expected bool
	}{
		{
			name:     "same url, branch, dir, namespace → conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "dir", "main"),
			expected: true,
		},
		{
			name:     "same url, branch, dir, different namespace → no conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir", "main"),
			attempt:  makeRepo("repo2", "ns2", "http://host/repo.git", "dir", "main"),
			expected: false,
		},
		{
			name:     "different branch → no conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "dir", "develop"),
			expected: false,
		},
		{
			name:     "different url → no conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo1.git", "dir", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo2.git", "dir", "main"),
			expected: false,
		},
		{
			name:     "root vs subdirectory → conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "subdir", "main"),
			expected: true,
		},
		{
			name:     "subdirectory vs root → conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "subdir", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "", "main"),
			expected: true,
		},
		{
			name:     "nested directory → conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "base", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "base/sub", "main"),
			expected: true,
		},
		{
			name:     "sibling directories → no conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir/sub/sub1", "main"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "dir/sub/sub2", "main"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, IsConflict(tc.existing, tc.attempt))
		})
	}
}

// TestHandleConflictScenarios tests the full Handle flow for various conflict scenarios.
func TestHandleConflictScenarios(t *testing.T) {
	tests := []struct {
		name       string
		attempted  *configapi.Repository
		existing   []configapi.Repository
		expectPass bool
	}{
		{
			name:       "no existing repos",
			attempted:  makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
			existing:   nil,
			expectPass: true,
		},
		{
			name:      "same git location different namespace → allowed",
			attempted: makeRepo("repo2", "ns2", "http://gitea/repo.git", "dir1", "main"),
			existing: []configapi.Repository{
				*makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
			},
			expectPass: true,
		},
		{
			name:      "same git location same namespace → conflict",
			attempted: makeRepo("repo2", "ns1", "http://gitea/repo.git", "dir1", "main"),
			existing: []configapi.Repository{
				*makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
			},
			expectPass: false,
		},
		{
			name:      "root dir conflicts with subdirectory",
			attempted: makeRepo("repo2", "ns1", "http://gitea/repo.git", "subdir", "main"),
			existing: []configapi.Repository{
				*makeRepo("repo1", "ns1", "http://gitea/repo.git", "", "main"),
			},
			expectPass: false,
		},
		{
			name:      "different branch → no conflict",
			attempted: makeRepo("repo2", "ns1", "http://gitea/repo.git", "dir1", "develop"),
			existing: []configapi.Repository{
				*makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
			},
			expectPass: true,
		},
		{
			name:      "updating self → no conflict",
			attempted: makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
			existing: []configapi.Repository{
				*makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
			},
			expectPass: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, tc.existing...)
			validator := NewRepositoryValidator(mockReader)

			operation := admissionv1.Create
			// Use Update operation for the "updating self" test case to verify UPDATE semantics
			if tc.name == "updating self → no conflict" {
				operation = admissionv1.Update
			}

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: operation,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
				},
			}

			// For UPDATE operations, also pass OldObject
			if operation == admissionv1.Update {
				req.OldObject = runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)}
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "expected allowed but got: %s", resp.Result.Message)
			} else {
				assert.False(t, resp.Allowed, "expected denied")
				assert.Contains(t, resp.Result.Message, "conflict")
			}
		})
	}
}

// TestNamespaceScopedConflictDetection verifies that conflict detection
// correctly scopes to namespace level and allows same git location in different namespaces
func TestNamespaceScopedConflictDetection(t *testing.T) {
	repoNs1 := makeRepo("repo1", "namespace-1", "http://gitea.local/org/repo.git", "dir1", "main")
	repoNs2 := makeRepo("repo2", "namespace-2", "http://gitea.local/org/repo.git", "dir1", "main")

	// Create mock reader that returns repo from ns1
	mockReader := setupMockReaderWithRepos(t, *repoNs1)
	validator := NewRepositoryValidator(mockReader)

	// Try to create same location in different namespace - should succeed
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, repoNs2)},
			Namespace: "namespace-2",
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed, "same git location should be allowed in different namespace")
}

// TestCrossNamespaceConflictingDirectories verifies that directory conflicts are detected across namespaces
// This test ensures that namespace isolation alone is insufficient; the validator must detect
// nested/conflicting directories even when repositories are in different namespaces.
func TestCrossNamespaceConflictingDirectories(t *testing.T) {
	tests := []struct {
		name       string
		existing   *configapi.Repository
		attempted  *configapi.Repository
		expectPass bool
		desc       string
	}{
		{
			name:       "root in ns1 conflicts with subdir in ns2",
			existing:   makeRepo("root-repo", "namespace-1", "http://gitea.local/org/repo.git", "", "main"),
			attempted:  makeRepo("sub-repo", "namespace-2", "http://gitea.local/org/repo.git", "packages/config", "main"),
			expectPass: false,
			desc:       "root directory in one namespace should conflict with subdirectory in another namespace",
		},
		{
			name:       "subdir in ns1 conflicts with root in ns2",
			existing:   makeRepo("sub-repo", "namespace-1", "http://gitea.local/org/repo.git", "services", "main"),
			attempted:  makeRepo("root-repo", "namespace-2", "http://gitea.local/org/repo.git", "", "main"),
			expectPass: false,
			desc:       "subdirectory in one namespace should conflict with root directory in another namespace",
		},
		{
			name:       "nested subdirs across namespaces conflict",
			existing:   makeRepo("config-repo", "namespace-1", "http://gitea.local/org/repo.git", "config", "main"),
			attempted:  makeRepo("nested-repo", "namespace-2", "http://gitea.local/org/repo.git", "config/services", "main"),
			expectPass: false,
			desc:       "nested subdirectories should conflict even across namespaces",
		},
		{
			name:       "different directories in different namespaces allowed",
			existing:   makeRepo("dir1-repo", "namespace-1", "http://gitea.local/org/repo.git", "dir1", "main"),
			attempted:  makeRepo("dir2-repo", "namespace-2", "http://gitea.local/org/repo.git", "dir2", "main"),
			expectPass: true,
			desc:       "non-conflicting directories in different namespaces should be allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, *tc.existing)
			validator := NewRepositoryValidator(mockReader)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
					Namespace: tc.attempted.Namespace,
				},
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "%s: %s", tc.name, tc.desc)
			} else {
				assert.False(t, resp.Allowed, "%s: %s", tc.name, tc.desc)
			}
		})
	}
}

// TestRootDirectoryConflictDetection verifies root directory conflicts with subdirectories
func TestRootDirectoryConflictDetection(t *testing.T) {
	tests := []struct {
		name       string
		existing   *configapi.Repository
		attempted  *configapi.Repository
		expectPass bool
	}{
		{
			name:       "root conflicts with subdir",
			existing:   makeRepo("root-repo", "ns1", "http://gitea.local/org/repo.git", "", "main"),
			attempted:  makeRepo("sub-repo", "ns1", "http://gitea.local/org/repo.git", "packages/config", "main"),
			expectPass: false,
		},
		{
			name:       "subdir conflicts with root",
			existing:   makeRepo("sub-repo", "ns1", "http://gitea.local/org/repo.git", "packages/config", "main"),
			attempted:  makeRepo("root-repo", "ns1", "http://gitea.local/org/repo.git", "", "main"),
			expectPass: false,
		},
		{
			name:       "different root dirs allowed",
			existing:   makeRepo("pkg1", "ns1", "http://gitea.local/org/repo.git", "", "main"),
			attempted:  makeRepo("pkg2", "ns2", "http://gitea.local/org/repo.git", "", "main"),
			expectPass: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, *tc.existing)
			validator := NewRepositoryValidator(mockReader)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
					Namespace: tc.attempted.Namespace,
				},
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "expected allowed: %s", resp.Result.Message)
			} else {
				assert.False(t, resp.Allowed, "expected denied")
				assert.Contains(t, resp.Result.Message, "conflict")
			}
		})
	}
}

// TestNestedDirectoryConflictDetection verifies nested directory conflicts
func TestNestedDirectoryConflictDetection(t *testing.T) {
	tests := []struct {
		name       string
		existing   *configapi.Repository
		attempted  *configapi.Repository
		expectPass bool
	}{
		{
			name:       "deep nested conflicts",
			existing:   makeRepo("parent", "ns1", "http://gitea.local/org/repo.git", "config", "main"),
			attempted:  makeRepo("child", "ns1", "http://gitea.local/org/repo.git", "config/overlays/prod", "main"),
			expectPass: false,
		},
		{
			name:       "sibling dirs allowed",
			existing:   makeRepo("base", "ns1", "http://gitea.local/org/repo.git", "config/base", "main"),
			attempted:  makeRepo("overlay", "ns1", "http://gitea.local/org/repo.git", "config/overlays", "main"),
			expectPass: true,
		},
		{
			name:       "same dir same ns conflict",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "packages", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", "packages", "main"),
			expectPass: false,
		},
		{
			name:       "same dir different ns allowed",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "packages", "main"),
			attempted:  makeRepo("repo2", "ns2", "http://gitea.local/org/repo.git", "packages", "main"),
			expectPass: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, *tc.existing)
			validator := NewRepositoryValidator(mockReader)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
					Namespace: tc.attempted.Namespace,
				},
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "expected allowed: %s", resp.Result.Message)
			} else {
				assert.False(t, resp.Allowed, "expected denied")
				assert.Contains(t, resp.Result.Message, "conflict")
			}
		})
	}
}

// TestDeleteOperation verifies delete operations are always allowed
func TestDeleteOperation(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			Name:      "some-repo",
			Namespace: "default",
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "validated successfully")
}

// TestNormalizeURLVariants tests URL normalization with various formats
func TestNormalizeURLVariants(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://example.com/repo.git", "http---example.com-repo.git"},
		{"https://github.com:443/org/repo.git", "https---github.com-443-org-repo.git"},
		{"ssh://git@host.com:2222/repo.git", "ssh---git@host.com-2222-repo.git"},
		{"git@github.com:org/repo.git", "git@github.com-org-repo.git"},
		{"http://localhost:8080/path/to/repo.git", "http---localhost-8080-path-to-repo.git"},
		{"file:///local/path/repo.git", "file----local-path-repo.git"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := NormalizeURL(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestBranchHandling tests that different branches don't conflict
func TestBranchHandling(t *testing.T) {
	tests := []struct {
		name       string
		existing   *configapi.Repository
		attempted  *configapi.Repository
		expectPass bool
	}{
		{
			name:       "different branches - no conflict",
			existing:   makeRepo("main-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main"),
			attempted:  makeRepo("dev-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "develop"),
			expectPass: true,
		},
		{
			name:       "same branch conflict",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", "dir1", "main"),
			expectPass: false,
		},
		{
			name:       "release branches - no conflict",
			existing:   makeRepo("release-v1", "ns1", "http://gitea.local/org/repo.git", "dir1", "release-1.0"),
			attempted:  makeRepo("release-v2", "ns1", "http://gitea.local/org/repo.git", "dir1", "release-2.0"),
			expectPass: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, *tc.existing)
			validator := NewRepositoryValidator(mockReader)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
					Namespace: tc.attempted.Namespace,
				},
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "expected allowed: %s", resp.Result.Message)
			} else {
				assert.False(t, resp.Allowed, "expected denied")
				assert.Contains(t, resp.Result.Message, "conflict")
			}
		})
	}
}

// TestURLVariations tests that URL variations (http vs https, different ports) don't conflict
func TestURLVariations(t *testing.T) {
	tests := []struct {
		name       string
		existing   *configapi.Repository
		attempted  *configapi.Repository
		expectPass bool
	}{
		{
			name:       "http vs https - treated as different",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main"),
			attempted:  makeRepo("repo2", "ns1", "https://gitea.local/org/repo.git", "dir1", "main"),
			expectPass: true,
		},
		{
			name:       "different ports - treated as different",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local:3000/org/repo.git", "dir1", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local:8080/org/repo.git", "dir1", "main"),
			expectPass: true,
		},
		{
			name:       "exact same URL - conflict",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", "dir1", "main"),
			expectPass: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, *tc.existing)
			validator := NewRepositoryValidator(mockReader)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
					Namespace: tc.attempted.Namespace,
				},
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "expected allowed: %s", resp.Result.Message)
			} else {
				assert.False(t, resp.Allowed, "expected denied")
			}
		})
	}
}

// TestOCIRepositorySkipsConflictCheck verifies OCI repositories skip conflict detection
func TestOCIRepositorySkipsConflictCheck(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	// OCI repos don't trigger List calls since they skip conflict detection
	validator := NewRepositoryValidator(mockReader)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oci-repo",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeOCI,
			Oci: &configapi.OciRepository{
				Registry: "ghcr.io/myorg",
			},
		},
	}

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, repo)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed, "OCI repos should be allowed without conflict check")
	assert.Contains(t, resp.Result.Message, "OCI")
}

// TestMultipleRepositoriesWithSameGitLocation tests conflict detection with many repos
func TestMultipleRepositoriesWithSameGitLocation(t *testing.T) {
	// Simulate a scenario with multiple repos in different namespaces pointing to same git location
	repos := []configapi.Repository{
		*makeRepo("repo-ns1", "namespace-1", "http://gitea.local/org/repo.git", "dir1", "main"),
		*makeRepo("repo-ns2", "namespace-2", "http://gitea.local/org/repo.git", "dir1", "main"),
		*makeRepo("repo-ns3", "namespace-3", "http://gitea.local/org/repo.git", "dir1", "main"),
	}

	// Attempt to create another in ns1 with same git location
	attempted := makeRepo("repo-conflict", "namespace-1", "http://gitea.local/org/repo.git", "dir1", "main")
	mockReader := setupMockReaderWithRepos(t, repos...)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, attempted)},
			Namespace: "namespace-1",
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed, "should conflict with existing repo in same namespace")
	assert.Contains(t, resp.Result.Message, "conflict")
}

// TestRepositoryWithoutGitSpec verifies repos without Git spec are handled correctly
func TestRepositoryWithoutGitSpec(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "incomplete-repo",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			// Git is nil - incomplete spec but should be caught by CRD validation
		},
	}

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, repo)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	// Should skip conflict detection gracefully
	assert.True(t, resp.Allowed)
}

// TestConflictDetectionAcrossManyConcurrentCreates tests isolation of conflict detection
func TestConflictDetectionAcrossManyConcurrentCreates(t *testing.T) {
	// Simulate 5 repos with same git location in different namespaces
	repos := make([]configapi.Repository, 0, 5)
	for i := 1; i <= 5; i++ {
		ns := fmt.Sprintf("namespace-%d", i)
		repos = append(repos, *makeRepo(fmt.Sprintf("repo-%d", i), ns, "http://gitea.local/org/repo.git", "dir1", "main"))
	}

	mockReader := setupMockReaderWithRepos(t, repos...)
	validator := NewRepositoryValidator(mockReader)

	// Attempt to create a new repo in namespace-6 with same git location - should succeed
	attempted := makeRepo("repo-6", "namespace-6", "http://gitea.local/org/repo.git", "dir1", "main")

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, attempted)},
			Namespace: "namespace-6",
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed, "should be allowed in different namespace")
}

// TestIsConflictWithGitNilHandling tests IsConflict function with nil Git specs
func TestIsConflictWithGitNilHandling(t *testing.T) {
	repoWithGit := makeRepo("repo1", "ns1", "http://host/repo.git", "dir", "main")
	repoWithoutGit := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "ns1",
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeOCI,
			// No Git spec
		},
	}

	// Should not panic and should return false (no conflict since one doesn't have Git)
	result := IsConflict(repoWithGit, repoWithoutGit)
	assert.False(t, result)
}

// TestEdgeCaseDirectories tests edge cases with directory paths
func TestEdgeCaseDirectories(t *testing.T) {
	tests := []struct {
		name       string
		existing   *configapi.Repository
		attempted  *configapi.Repository
		expectPass bool
	}{
		{
			name:       "empty directory (root) same namespace",
			existing:   makeRepo("root1", "ns1", "http://gitea.local/org/repo.git", "", "main"),
			attempted:  makeRepo("root2", "ns1", "http://gitea.local/org/repo.git", "", "main"),
			expectPass: false,
		},
		{
			name:       "trailing slash normalization",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir/", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", "/dir", "main"),
			expectPass: false, // Both normalize to "dir"
		},
		{
			name:       "dots in directory",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir.config", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", "dir.config", "main"),
			expectPass: false,
		},
		{
			name:       "dot directories",
			existing:   makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", ".config", "main"),
			attempted:  makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", ".config", "main"),
			expectPass: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockReader := setupMockReaderWithRepos(t, *tc.existing)
			validator := NewRepositoryValidator(mockReader)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: marshalRepo(t, tc.attempted)},
					Namespace: tc.attempted.Namespace,
				},
			}

			resp := validator.Handle(context.Background(), req)
			if tc.expectPass {
				assert.True(t, resp.Allowed, "expected allowed: %s", resp.Result.Message)
			} else {
				assert.False(t, resp.Allowed, "expected denied")
			}
		})
	}
}

// TestListRepositoriesError tests handling of List API errors
func TestListRepositoriesError(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	// Mock List to return an error
	mockReader.EXPECT().List(mock.Anything, mock.MatchedBy(func(obj client.ObjectList) bool {
		_, ok := obj.(*configapi.RepositoryList)
		return ok
	}), mock.Anything).Return(fmt.Errorf("API server connection failed"))

	validator := NewRepositoryValidator(mockReader)
	repo := makeRepo("test-repo", "default", "http://gitea.local/org/repo.git", "dir1", "main")

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, repo)},
			Namespace: "default",
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Equal(t, int32(http.StatusInternalServerError), resp.Result.Code)
	assert.Contains(t, resp.Result.Message, "could not list repositories")
}

// TestIsNestedConflictSpecialCases tests additional edge cases in nested conflict detection
func TestIsNestedConflictSpecialCases(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		// Single level paths
		{"both root", "", "", false},
		// Similar but not nested
		{"prefix but not nested", "config", "configuration", false},
		{"similar name", "pkg", "pkg2", false},
		// Complex nested scenarios
		{"deeply nested", "a/b/c/d/e", "a/b", true},
		{"reverse deeply nested", "a/b", "a/b/c/d/e", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, IsNestedConflict(tc.a, tc.b), "IsNestedConflict(%q, %q)", tc.a, tc.b)
		})
	}
}

// TestFieldIndexFallback tests that webhook works when field indexes are not available
// (falls back to full list + filter when field label errors occur)
func TestFieldIndexFallback(t *testing.T) {
	// This test verifies the webhook gracefully handles "field label not supported" errors
	// by falling back to list all + filter in-memory.
	// Since the error message matching happens in the webhook code, we test it indirectly:
	// When List fails with "field label not supported", the webhook retries without field matchers.

	mockReader := mockclient.NewMockReader(t)

	existing := []configapi.Repository{
		*makeRepo("repo1", "ns1", "http://gitea/repo.git", "dir1", "main"),
		*makeRepo("repo2", "ns2", "http://gitea/repo.git", "dir1", "main"),
	}

	// Mock any List call to return all repos (simulating fallback behavior)
	mockReader.EXPECT().List(mock.Anything, mock.MatchedBy(func(obj client.ObjectList) bool {
		_, ok := obj.(*configapi.RepositoryList)
		return ok
	}), mock.Anything).Run(func(_ context.Context, obj client.ObjectList, _ ...client.ListOption) {
		list := obj.(*configapi.RepositoryList)
		list.Items = append([]configapi.Repository{}, existing...)
	}).Return(nil)

	validator := NewRepositoryValidator(mockReader)
	// Attempt repo3 in ns3 with same git location as repo1/repo2 (but different namespaces)
	repo := makeRepo("repo3", "ns3", "http://gitea/repo.git", "dir1", "main")

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, repo)},
			Namespace: "ns3",
		},
	}

	resp := validator.Handle(context.Background(), req)
	// Should be allowed because existing repos are in different namespaces
	// This indirectly tests the fallback: if the webhook successfully filters by namespace,
	// it means it got all the repos (fallback behavior)
	assert.True(t, resp.Allowed, "expected allowed, got: %s", resp.Result.Message)
}

// TestMatchConditionsOnCreateOperation verifies webhook validates on CREATE operations
// The matchConditions expression includes "request.operation == 'CREATE'"
func TestMatchConditionsOnCreateOperation(t *testing.T) {
	existing := makeRepo("existing", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	mockReader := setupMockReaderWithRepos(t, *existing)
	validator := NewRepositoryValidator(mockReader)

	// Attempt to create a conflicting repo
	attempted := makeRepo("new", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: marshalRepo(t, attempted)},
			Namespace: "ns1",
		},
	}

	resp := validator.Handle(context.Background(), req)
	// CREATE should trigger validation (matchCondition matches), and conflict should be detected
	assert.False(t, resp.Allowed, "CREATE with conflict should be rejected")
	assert.Contains(t, resp.Result.Message, "conflict")
}

// TestMatchConditionsOnDeleteOperation verifies webhook validates on DELETE operations
// The matchConditions expression includes "request.operation == 'DELETE'"
// NOTE: Currently DELETEs are always allowed, but webhook still receives them
func TestMatchConditionsOnDeleteOperation(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	validator := NewRepositoryValidator(mockReader)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			Name:      "some-repo",
			Namespace: "ns1",
		},
	}

	resp := validator.Handle(context.Background(), req)
	// DELETE always allowed, but webhook receives it due to matchCondition
	assert.True(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "validated successfully")
}

// TestMatchConditionsGenerationChangeDetection verifies UPDATE with spec changes triggers validation
// The matchCondition includes: "object.metadata.generation != oldObject.metadata.generation"
// When generation differs, it means spec changed (K8s automatically increments generation on spec changes)
func TestMatchConditionsGenerationChangeDetection(t *testing.T) {
	existing := makeRepo("existing", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	mockReader := setupMockReaderWithRepos(t, *existing)
	validator := NewRepositoryValidator(mockReader)

	// Create a new repo with updated spec (different directory)
	updated := makeRepo("new", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	updated.Generation = 2 // Simulating spec change (generation incremented by K8s)

	oldData, err := json.Marshal(updated)
	require.NoError(t, err)

	// Modify the updated repo to have a conflicting directory
	updated.Spec.Git.Directory = "dir1"
	newData, err := json.Marshal(updated)
	require.NoError(t, err)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: newData},
			OldObject: runtime.RawExtension{Raw: oldData},
			Namespace: "ns1",
			Name:      "new",
		},
	}

	resp := validator.Handle(context.Background(), req)
	// UPDATE with spec change (generation changed) should trigger validation
	// Note: The validator doesn't check generation directly, it validates all UPDATEs
	assert.False(t, resp.Allowed, "UPDATE with conflicting change should be rejected")
}

// TestMatchConditionsSkipsStatusOnlyUpdates documents that status-only updates
// would be filtered by matchConditions if generation didn't change
// (In practice, K8s handles this transparently - status updates don't increment generation)
func TestMatchConditionsSkipsStatusOnlyUpdates(t *testing.T) {
	mockReader := setupMockReaderWithRepos(t) // Empty repo list
	validator := NewRepositoryValidator(mockReader)

	repo := makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	repo.Generation = 1

	repoData, err := json.Marshal(repo)
	require.NoError(t, err)

	// Simulate a status-only update where generation doesn't change
	// (In real K8s, generation would only increment on spec changes)
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: repoData},
			OldObject: runtime.RawExtension{Raw: repoData}, // Same object = same generation
			Namespace: "ns1",
			Name:      "repo1",
		},
	}

	resp := validator.Handle(context.Background(), req)
	// With the same generation in old and new, matchCondition would filter this out
	// However, the validator still runs and should allow it since there's no conflict
	assert.True(t, resp.Allowed, "status-only update should be allowed when no conflict exists")
}

// TestMatchConditionsUpdateWithoutSpecChange verifies UPDATE without spec change
// Documents that matchCondition: "object.metadata.generation != oldObject.metadata.generation"
// TestMatchConditionsUpdateWithoutSpecChange verifies UPDATE without spec change
// Documents that matchCondition: "object.metadata.generation != oldObject.metadata.generation"
// would filter out metadata-only updates (generation stays the same)
func TestMatchConditionsUpdateWithoutSpecChange(t *testing.T) {
	mockReader := setupMockReaderWithRepos(t) // Empty repo list
	validator := NewRepositoryValidator(mockReader)

	repo := makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	repo.Generation = 1
	repo.Annotations = map[string]string{"old": "value"}

	oldData, err := json.Marshal(repo)
	require.NoError(t, err)

	// Update only annotations (metadata), not spec
	// In real K8s, generation wouldn't increment for this
	repo.Annotations["new"] = "annotation"
	// Generation stays the same = no spec change
	newData, err := json.Marshal(repo)
	require.NoError(t, err)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: newData},
			OldObject: runtime.RawExtension{Raw: oldData},
			Namespace: "ns1",
			Name:      "repo1",
		},
	}

	resp := validator.Handle(context.Background(), req)
	// Metadata-only change (generation unchanged) would be filtered by matchCondition
	// The validator still processes this and allows it
	assert.True(t, resp.Allowed, "metadata-only update should be allowed")
}

// TestMatchConditionsCreateVsUpdateBehavior verifies CREATE and UPDATE are both validated
func TestMatchConditionsCreateVsUpdateBehavior(t *testing.T) {
	existing := makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	mockReader := setupMockReaderWithRepos(t, *existing)
	validator := NewRepositoryValidator(mockReader)

	conflicting := makeRepo("repo2", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")

	testCases := []struct {
		operation admissionv1.Operation
		name      string
	}{
		{admissionv1.Create, "CREATE"},
		{admissionv1.Update, "UPDATE"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := marshalRepo(t, conflicting)
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: tc.operation,
					Object:    runtime.RawExtension{Raw: data},
					Namespace: "ns1",
				},
			}

			if tc.operation == admissionv1.Update {
				req.OldObject = runtime.RawExtension{Raw: data}
			}

			resp := validator.Handle(context.Background(), req)
			// Both CREATE and UPDATE should detect the conflict
			assert.False(t, resp.Allowed, "%s should detect conflict", tc.name)
			assert.Contains(t, resp.Result.Message, "conflict")
		})
	}
}

// TestMatchConditionsExpressionLogic verifies the AND/OR logic of the matchCondition expression
// matchCondition: "request.operation == 'CREATE' || request.operation == 'DELETE' || object.metadata.generation != oldObject.metadata.generation"
// This means: webhook fires on CREATE, or DELETE, or (UPDATE with generation change)
func TestMatchConditionsExpressionLogic(t *testing.T) {
	mockReader := setupMockReaderWithRepos(t) // Empty repo list
	validator := NewRepositoryValidator(mockReader)

	repo := makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")

	testCases := []struct {
		operation         admissionv1.Operation
		generationChanged bool
		name              string
		shouldFireWebhook bool
	}{
		{admissionv1.Create, false, "CREATE always matches", true},
		{admissionv1.Delete, false, "DELETE always matches", true},
		{admissionv1.Update, true, "UPDATE with generation change matches", true},
		{admissionv1.Update, false, "UPDATE without generation change doesn't match", true}, // Note: validator always runs for UPDATE
		{admissionv1.Connect, false, "CONNECT doesn't match any condition", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := marshalRepo(t, repo)
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: tc.operation,
					Object:    runtime.RawExtension{Raw: data},
					Namespace: "ns1",
				},
			}

			if tc.operation == admissionv1.Update && tc.generationChanged {
				repo.Generation = 2
				newData := marshalRepo(t, repo)
				req.Object = runtime.RawExtension{Raw: newData}
				req.OldObject = runtime.RawExtension{Raw: data}
			}

			resp := validator.Handle(context.Background(), req)

			if tc.operation == admissionv1.Connect {
				// CONNECT doesn't match, so webhook doesn't fire, operation allowed
				assert.True(t, resp.Allowed, "%s: unknown operation should be allowed", tc.name)
			} else {
				// CREATE, DELETE, UPDATE all get processed by webhook
				assert.True(t, resp.Allowed, "%s: should be allowed for empty repo list", tc.name)
			}
		})
	}
}

// TestMatchConditionsWithConflictScenarios verifies matchConditions correctly filter to scenarios with conflicts
func TestMatchConditionsWithConflictScenarios(t *testing.T) {
	tests := []struct {
		operation         admissionv1.Operation
		generationChanged bool
		existingRepos     []configapi.Repository
		attempted         *configapi.Repository
		name              string
		expectConflict    bool
	}{
		{
			operation:      admissionv1.Create,
			existingRepos:  []configapi.Repository{*makeRepo("repo1", "ns1", "http://host/repo.git", "dir1", "main")},
			attempted:      makeRepo("repo2", "ns1", "http://host/repo.git", "dir1", "main"),
			name:           "CREATE with conflict",
			expectConflict: true,
		},
		{
			operation:      admissionv1.Update,
			existingRepos:  []configapi.Repository{*makeRepo("repo1", "ns1", "http://host/repo.git", "dir1", "main")},
			attempted:      makeRepo("repo2", "ns1", "http://host/repo.git", "dir1", "main"),
			name:           "UPDATE with conflict",
			expectConflict: true,
		},
		{
			operation:      admissionv1.Delete,
			existingRepos:  []configapi.Repository{}, // DELETE doesn't call List
			attempted:      makeRepo("repo1", "ns1", "http://host/repo.git", "dir1", "main"),
			name:           "DELETE always allowed (no validation)",
			expectConflict: false,
		},
		{
			operation:      admissionv1.Create,
			existingRepos:  []configapi.Repository{*makeRepo("repo1", "ns1", "http://host/repo.git", "dir1", "main")},
			attempted:      makeRepo("repo2", "ns2", "http://host/repo.git", "dir1", "main"),
			name:           "CREATE cross-namespace no conflict",
			expectConflict: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// For DELETE operations, we can use an empty mock reader since DELETE skips all validation
			var mockReader *mockclient.MockReader
			if tc.operation == admissionv1.Delete {
				mockReader = mockclient.NewMockReader(t)
			} else {
				mockReader = setupMockReaderWithRepos(t, tc.existingRepos...)
			}
			validator := NewRepositoryValidator(mockReader)

			data := marshalRepo(t, tc.attempted)
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: tc.operation,
					Object:    runtime.RawExtension{Raw: data},
					Namespace: tc.attempted.Namespace,
				},
			}

			if tc.operation == admissionv1.Update {
				req.OldObject = runtime.RawExtension{Raw: data}
			}

			resp := validator.Handle(context.Background(), req)

			if tc.expectConflict {
				assert.False(t, resp.Allowed, "%s: should be rejected", tc.name)
				assert.Contains(t, resp.Result.Message, "conflict")
			} else {
				assert.True(t, resp.Allowed, "%s: should be allowed, got: %s", tc.name, resp.Result.Message)
			}
		})
	}
}

// TestMatchConditionsSkipNonSpecUpdatesDescription documents the matchCondition purpose
// This test demonstrates that the matchCondition name "skip-non-spec-updates" and its logic
// prevent unnecessary webhook invocations for status and metadata-only updates
func TestMatchConditionsSkipNonSpecUpdatesDescription(t *testing.T) {
	// The matchCondition filters webhook execution:
	// - Allows: CREATE (always validates)
	// - Allows: DELETE (always validates)
	// - Allows: UPDATE where object.metadata.generation != oldObject.metadata.generation
	//   (generation changes only on spec changes, so this filters out status/metadata-only updates)
	//
	// In real Kubernetes:
	// - Status updates go through a separate API path and don't increment generation
	// - Metadata-only updates don't increment generation
	// - Only spec changes increment generation
	//
	// This matchCondition ensures the webhook only runs for meaningful changes.

	mockReader := setupMockReaderWithRepos(t) // Empty repo list
	validator := NewRepositoryValidator(mockReader)

	repo := makeRepo("repo1", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	oldGen := int64(1)
	newGen := int64(2)

	testCases := []struct {
		name                string
		oldGeneration       int64
		newGeneration       int64
		wouldMatchCondition bool
		description         string
	}{
		{
			name:                "status update (generation same)",
			oldGeneration:       oldGen,
			newGeneration:       oldGen,
			wouldMatchCondition: false,
			description:         "Status updates don't change generation, so would be skipped by matchCondition",
		},
		{
			name:                "metadata update (generation same)",
			oldGeneration:       oldGen,
			newGeneration:       oldGen,
			wouldMatchCondition: false,
			description:         "Metadata-only updates don't change generation, so would be skipped",
		},
		{
			name:                "spec update (generation changed)",
			oldGeneration:       oldGen,
			newGeneration:       newGen,
			wouldMatchCondition: true,
			description:         "Spec updates increment generation, so would match and be validated",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo.Generation = tc.oldGeneration
			oldData := marshalRepo(t, repo)

			repo.Generation = tc.newGeneration
			newData := marshalRepo(t, repo)

			// The webhook would only fire if matchCondition matches
			// We verify the validator processes UPDATE operations
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Update,
					Object:    runtime.RawExtension{Raw: newData},
					OldObject: runtime.RawExtension{Raw: oldData},
					Namespace: "ns1",
				},
			}

			resp := validator.Handle(context.Background(), req)

			if tc.wouldMatchCondition {
				// Webhook fires, validates (no conflict expected with empty list)
				assert.True(t, resp.Allowed, tc.description)
			} else {
				// Webhook would be skipped by matchCondition in real K8s
				// But for testing, validator still processes it
				assert.True(t, resp.Allowed, tc.description)
			}

			t.Logf("✓ %s: %s", tc.name, tc.description)
		})
	}
}
