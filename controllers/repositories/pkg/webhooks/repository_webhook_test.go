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

	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

// TestHandleDelete verifies DELETE operations are always allowed.
func TestHandleDelete(t *testing.T) {
	validator := NewRepositoryValidator(fake.NewClientBuilder().WithScheme(newScheme()).Build())

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
	validator := NewRepositoryValidator(fake.NewClientBuilder().WithScheme(newScheme()).Build())

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
	validator := NewRepositoryValidator(fake.NewClientBuilder().WithScheme(newScheme()).Build())

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
	validator := NewRepositoryValidator(fake.NewClientBuilder().WithScheme(newScheme()).Build())

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
	scheme := newScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := NewRepositoryValidator(client)

	repo := makeRepo("new-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")

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
	scheme := newScheme()
	existing := makeRepo("existing-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	validator := NewRepositoryValidator(client)

	attempted := makeRepo("new-repo", "ns1", "http://gitea.local/org/repo.git", "dir1", "main")

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
	scheme := newScheme()
	existing := makeRepo("existing-repo", "ns1", "http://gitea.local/org/repo.git", "", "main")
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	validator := NewRepositoryValidator(client)

	// Attempting to update a different repo to use a subdirectory of the root repo
	attempted := makeRepo("other-repo", "ns1", "http://gitea.local/org/repo.git", "subdir", "main")

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
			scheme := newScheme()
			objs := make([]client.Object, 0, len(tc.existing))
			for i := range tc.existing {
				objs = append(objs, &tc.existing[i])
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			validator := NewRepositoryValidator(cl)

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
	scheme := newScheme()
	repoNs1 := makeRepo("repo1", "namespace-1", "http://gitea.local/org/repo.git", "dir1", "main")
	repoNs2 := makeRepo("repo2", "namespace-2", "http://gitea.local/org/repo.git", "dir1", "main")

	// Create client with repo in ns1
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repoNs1).Build()
	validator := NewRepositoryValidator(client)

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
			scheme := newScheme()
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existing).Build()
			validator := NewRepositoryValidator(client)

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
			scheme := newScheme()
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existing).Build()
			validator := NewRepositoryValidator(client)

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
	validator := NewRepositoryValidator(fake.NewClientBuilder().Build())

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
			scheme := newScheme()
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existing).Build()
			validator := NewRepositoryValidator(client)

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
			scheme := newScheme()
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existing).Build()
			validator := NewRepositoryValidator(client)

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

// TestIsSecretRefChanged tests secret reference change detection
