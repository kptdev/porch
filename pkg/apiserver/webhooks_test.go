// Copyright 2022,2024 The kpt Authors
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

package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreateCerts(t *testing.T) {
	webhookCfg := WebhookConfig{
		RepoHost:       "localhost",
		CertStorageDir: t.TempDir(),
	}
	defer func() {
		require.NoError(t, os.RemoveAll(webhookCfg.CertStorageDir))
	}()

	caCert, err := createCerts(&webhookCfg)
	require.NoError(t, err)

	caStr := strings.TrimSpace(string(caCert))
	require.True(t, strings.HasPrefix(caStr, "-----BEGIN CERTIFICATE-----\n"))
	require.True(t, strings.HasSuffix(caStr, "\n-----END CERTIFICATE-----"))

	crt, err := os.ReadFile(filepath.Join(webhookCfg.CertStorageDir, "tls.crt"))
	require.NoError(t, err)

	key, err := os.ReadFile(filepath.Join(webhookCfg.CertStorageDir, "tls.key"))
	require.NoError(t, err)

	crtStr := strings.TrimSpace(string(crt))
	require.True(t, strings.HasPrefix(crtStr, "-----BEGIN CERTIFICATE-----\n"))
	require.True(t, strings.HasSuffix(crtStr, "\n-----END CERTIFICATE-----"))

	keyStr := strings.TrimSpace(string(key))
	require.True(t, strings.HasPrefix(keyStr, "-----BEGIN RSA PRIVATE KEY-----\n"))
	require.True(t, strings.HasSuffix(keyStr, "\n-----END RSA PRIVATE KEY-----"))
}

func TestLoadCertificate(t *testing.T) {

	//what do i need to test.
	// first create dummy certs for testing
	webhookCfg := WebhookConfig{
		RepoHost:       "localhost",
		CertStorageDir: t.TempDir(),
	}
	defer func() {
		require.NoError(t, os.RemoveAll(webhookCfg.CertStorageDir))
	}()

	_, err := createCerts(&webhookCfg)
	require.NoError(t, err)

	//1. test file that cannot be os.stated or causes that function to fail
	_, err1 := loadCertificate(filepath.Join(webhookCfg.CertStorageDir, "nonexistingcrtfile.key"), filepath.Join(webhookCfg.CertStorageDir, "nonexistingkeyfile.key"))
	require.Error(t, err1)
	// reseting back to 0
	certModTime = time.Time{}
	//2. test happy path of os.stat and continue to next error
	//3. test loading good cert happy path
	keypath := filepath.Join(webhookCfg.CertStorageDir, "tls.key")
	crtpath := filepath.Join(webhookCfg.CertStorageDir, "tls.crt")

	_, err2 := loadCertificate(crtpath, keypath)
	require.NoError(t, err2)
	certModTime = time.Time{}

	//4. test loading faulty cert error
	data := []byte("Hello, World!")

	writeErr := os.WriteFile(keypath, data, 0644)
	require.NoError(t, writeErr)
	writeErr2 := os.WriteFile(crtpath, data, 0644)
	require.NoError(t, writeErr2)

	_, err3 := loadCertificate(filepath.Join(webhookCfg.CertStorageDir, "tls.crt"), filepath.Join(webhookCfg.CertStorageDir, "tls.key"))
	require.Error(t, err3)
	certModTime = time.Time{}
}

// method for capturing klog error's
func captureStderr(f func()) string {
	read, write, _ := os.Pipe()
	stderr := os.Stderr
	os.Stderr = write
	outputChannel := make(chan string)

	// Copy the output in a separate goroutine so printing can't block indefinitely.
	go func() {
		var buf bytes.Buffer
		//nolint:errcheck
		io.Copy(&buf, read)
		outputChannel <- buf.String()
	}()

	// Run the provided function and capture the stderr output
	f()

	// Restore the original stderr and close the write-end of the pipe so the goroutine will exit
	os.Stderr = stderr
	write.Close()
	out := <-outputChannel

	return out
}

func TestWatchCertificatesInvalidDirectory(t *testing.T) {
	// method for processing klog output
	assertLogMessages := func(log string) error {
		if len(log) > 0 {
			if log[0] == 'E' || log[0] == 'W' || log[0] == 'F' {
				return errors.New("Error Occurred in Watcher")
			}
		}
		return nil
	}
	// Set up the temp directory with dummy certificate files
	webhookCfg := WebhookConfig{
		RepoHost:       "localhost",
		CertStorageDir: t.TempDir(),
	}
	defer func() {
		require.NoError(t, os.RemoveAll(webhookCfg.CertStorageDir))
	}()

	_, err := createCerts(&webhookCfg)
	require.NoError(t, err)

	keyFile := filepath.Join(webhookCfg.CertStorageDir, "tls.key")
	certFile := filepath.Join(webhookCfg.CertStorageDir, "tls.crt")

	// Create a context with a cancel function
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	invalid_watch_entity_logs := captureStderr(func() {
		// firstly test error occurring from invalid entity for watcher to watch. aka invalid dir, err expected
		go watchCertificates(ctx, "/random-dir-that-does-not-exist", certFile, keyFile)
		time.Sleep(5 * time.Second) // Give some time for the logs to be flushed
	})
	t.Log(invalid_watch_entity_logs)
	err = assertLogMessages(invalid_watch_entity_logs)
	require.Error(t, err)
}

func TestWatchCertificatesSuccessfulReload(t *testing.T) {
	// method for processing klog output
	assertLogMessages := func(log string) error {
		if len(log) > 0 {
			if log[0] == 'E' || log[0] == 'W' || log[0] == 'F' {
				return errors.New("Error Occured in Watcher")
			}
		}
		return nil
	}
	// Set up the temp directory with dummy certificate files
	webhookCfg := WebhookConfig{
		RepoHost:       "localhost",
		CertStorageDir: t.TempDir(),
	}
	defer func() {
		require.NoError(t, os.RemoveAll(webhookCfg.CertStorageDir))
	}()

	_, err := createCerts(&webhookCfg)
	require.NoError(t, err)

	keyFile := filepath.Join(webhookCfg.CertStorageDir, "tls.key")
	certFile := filepath.Join(webhookCfg.CertStorageDir, "tls.crt")

	// Create a context with a cancel function
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	valid_reload_logs := captureStderr(func() {
		go watchCertificates(ctx, webhookCfg.CertStorageDir, certFile, keyFile)
		// give some time for watchCertificates method to spin up
		time.Sleep(3 * time.Second)

		//create file to trigger change but not alter the certificate contents
		//should trigger reload and certificate reloaded successfully
		newFilePath := filepath.Join(webhookCfg.CertStorageDir, "new_temp_file.txt")
		_, err = os.Create(newFilePath)
		require.NoError(t, err)

		time.Sleep(5 * time.Second) // Give some time for the logs to be flushed
	})
	t.Log(valid_reload_logs)
	err = assertLogMessages(valid_reload_logs)
	require.NoError(t, err)
}

func TestWatchCertificatesInvalidCertReload(t *testing.T) {

	// method for processing klog output
	assertLogMessages := func(log string) error {
		if len(log) > 0 {
			if log[0] == 'E' || log[0] == 'W' || log[0] == 'F' {
				return errors.New("Error Occured in Watcher")
			}
		}
		return nil
	}
	// Set up the temp directory with dummy certificate files
	webhookCfg := WebhookConfig{
		RepoHost:       "localhost",
		CertStorageDir: t.TempDir(),
	}
	defer func() {
		require.NoError(t, os.RemoveAll(webhookCfg.CertStorageDir))
	}()

	_, err := createCerts(&webhookCfg)
	require.NoError(t, err)

	keyFile := filepath.Join(webhookCfg.CertStorageDir, "tls.key")
	certFile := filepath.Join(webhookCfg.CertStorageDir, "tls.crt")

	// Create a context with a cancel function
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	invalid_reload_logs := captureStderr(func() {
		go watchCertificates(ctx, webhookCfg.CertStorageDir, certFile, keyFile)
		// give some time for watchCertificates method to spin up
		time.Sleep(3 * time.Second)

		// Modify the certificate file to trigger a file system event
		// should cause an error log since cert contents are not valid anymore
		certModTime = time.Time{}
		err = os.WriteFile(certFile, []byte("dummy text"), 0660)
		require.NoError(t, err)

		time.Sleep(5 * time.Second)
	})
	t.Log(invalid_reload_logs)
	err = assertLogMessages(invalid_reload_logs)
	require.Error(t, err)
}

func TestWatchCertificatesGracefulTermination(t *testing.T) {
	// method for processing klog output
	assertLogMessages := func(log string) error {
		if len(log) > 0 {
			if log[0] == 'E' || log[0] == 'W' || log[0] == 'F' {
				return errors.New("Error Occured in Watcher")
			}
		}
		return nil
	}
	// Set up the temp directory with dummy certificate files
	webhookCfg := WebhookConfig{
		RepoHost:       "localhost",
		CertStorageDir: t.TempDir(),
	}
	defer func() {
		require.NoError(t, os.RemoveAll(webhookCfg.CertStorageDir))
	}()

	_, err := createCerts(&webhookCfg)
	require.NoError(t, err)

	keyFile := filepath.Join(webhookCfg.CertStorageDir, "tls.key")
	certFile := filepath.Join(webhookCfg.CertStorageDir, "tls.crt")

	// Create a context with a cancel function
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	graceful_termination_logs := captureStderr(func() {
		go watchCertificates(ctx, webhookCfg.CertStorageDir, certFile, keyFile)
		// give some time for watchCertificates method to spin up
		time.Sleep(3 * time.Second)

		// trigger graceful termination
		cancel()

		time.Sleep(5 * time.Second)
	})
	t.Log(graceful_termination_logs)
	err = assertLogMessages(graceful_termination_logs)
	require.NoError(t, err)
}

func TestValidateRepository(t *testing.T) {
	scheme := runtime.NewScheme()
	configapi.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	t.Run("invalid content-type", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, repositoryValidationEndpoint, nil)
		require.NoError(t, err)
		request.Header.Set("Content-Type", "foo")
		response := httptest.NewRecorder()

		validateRepository(response, request, fakeClient)
		require.Equal(t,
			"error decoding admission review: expected Content-Type 'application/json'",
			response.Body.String())
	})

	t.Run("valid content-type, but no body", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, repositoryValidationEndpoint, nil)
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()

		validateRepository(response, request, fakeClient)
		require.Equal(t,
			"error decoding admission review: admission review request is empty",
			response.Body.String())
	})

	t.Run("unexpected resource type", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, repositoryValidationEndpoint, nil)
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")

		admissionReviewRequest := admissionv1.AdmissionReview{
			TypeMeta: v1.TypeMeta{
				Kind:       "AdmissionReview",
				APIVersion: "admission.k8s.io/v1",
			},
			Request: &admissionv1.AdmissionRequest{
				Resource: v1.GroupVersionResource{
					Group:    "config.porch.kpt.dev",
					Version:  "v1alpha1",
					Resource: "not-repositories",
				},
			},
		}

		body, err := json.Marshal(admissionReviewRequest)
		require.NoError(t, err)

		request.Body = io.NopCloser(bytes.NewReader(body))
		response := httptest.NewRecorder()

		validateRepository(response, request, fakeClient)
		require.Equal(t,
			"unexpected resource: not-repositories",
			response.Body.String())
	})

	t.Run("delete operation skips unmarshal and allows", func(t *testing.T) {
		admissionReview := admissionv1.AdmissionReview{
			TypeMeta: v1.TypeMeta{
				Kind:       "AdmissionReview",
				APIVersion: "admission.k8s.io/v1",
			},
			Request: &admissionv1.AdmissionRequest{
				UID: "delete-123",
				Resource: v1.GroupVersionResource{
					Group:    "config.porch.kpt.dev",
					Version:  "v1alpha1",
					Resource: "repositories",
				},
				Operation: admissionv1.Delete,
				Name:      "my-repo",
				Namespace: "test-ns",
				// Object.Raw is empty on DELETE — this is the scenario that was failing
				OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"my-repo","namespace":"test-ns"}}`)},
			},
		}
		body, err := json.Marshal(admissionReview)
		require.NoError(t, err)

		request, err := http.NewRequest(http.MethodPost, repositoryValidationEndpoint, bytes.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")

		response := httptest.NewRecorder()
		validateRepository(response, request, fakeClient)

		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), "Repository deletion validated successfully")
	})

	createAdmissionReview := func(name, namespace, gitURL, directory, branch string) []byte {
		repo := configapi.Repository{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: configapi.RepositorySpec{
				Git: &configapi.GitRepository{
					Repo:      gitURL,
					Directory: directory,
					Branch:    branch,
				},
			},
		}
		rawRepo, err := json.Marshal(repo)
		require.NoError(t, err)

		admissionReview := admissionv1.AdmissionReview{
			TypeMeta: v1.TypeMeta{
				Kind:       "AdmissionReview",
				APIVersion: "admission.k8s.io/v1",
			},
			Request: &admissionv1.AdmissionRequest{
				UID: "12345",
				Resource: v1.GroupVersionResource{
					Group:    "config.porch.kpt.dev",
					Version:  "v1alpha1",
					Resource: "repositories",
				},
				Object:    runtime.RawExtension{Raw: rawRepo},
				Name:      name,
				Namespace: namespace,
			},
		}
		body, err := json.Marshal(admissionReview)
		require.NoError(t, err)
		return body
	}
	tests := []struct {
		name       string
		repoName   string
		namespace  string
		gitURL     string
		directory  string
		branch     string
		setupRepos []configapi.Repository
		expectOK   bool
	}{
		{
			name:      "valid repository creation",
			repoName:  "repo1",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir1",
			branch:    "main",
			expectOK:  true,
		},
		{
			name:      "same git url and directory in different namespace",
			repoName:  "repo2",
			namespace: "ns2",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir1",
			branch:    "main",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "dir1", Branch: "main"},
					},
				},
			},
			expectOK: true,
		},
		{
			name:      "same git url different directory in same namespace",
			repoName:  "repo3",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir2",
			branch:    "main",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "dir1", Branch: "main"},
					},
				},
			},
			expectOK: true,
		},
		{
			name:      "same url, directory different branch - no conflict",
			repoName:  "repo4",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir1",
			branch:    "develop",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "dir1", Branch: "main"},
					},
				},
			},
			expectOK: true,
		},
		{
			name:      "conflict: same url, branch, directory, and namespace",
			repoName:  "repo5",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir1",
			branch:    "main",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "dir1", Branch: "main"},
					},
				},
			},
			expectOK: false,
		},
		{
			name:      "conflict: root directory vs subdirectory",
			repoName:  "repo6",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "subdir",
			branch:    "main",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "", Branch: "main"},
					},
				},
			},
			expectOK: false,
		},
		{
			name:      "conflict: nested directory",
			repoName:  "repo7",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "base/sub",
			branch:    "main",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "base", Branch: "main"},
					},
				},
			},
			expectOK: false,
		},
		{
			name:      "Valid: same url, directory, namespace different branch",
			repoName:  "repo8",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir1",
			branch:    "myBranch",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "dir1", Branch: "main"},
					},
				},
			},
			expectOK: true,
		},
		{
			name:      "conflict: branch defaults to main (CRD handles defaulting)",
			repoName:  "repo9",
			namespace: "ns1",
			gitURL:    "http://gitea.local/myrepo.git",
			directory: "dir1",
			branch:    "main",
			setupRepos: []configapi.Repository{
				{
					ObjectMeta: v1.ObjectMeta{Name: "repo1", Namespace: "ns1"},
					Spec: configapi.RepositorySpec{
						Git: &configapi.GitRepository{Repo: "http://gitea.local/myrepo.git", Directory: "dir1", Branch: "main"},
					},
				},
			},
			expectOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a fresh client for each test
			scheme := runtime.NewScheme()
			configapi.AddToScheme(scheme)
			testClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			// Setup existing repositories
			for _, repo := range tc.setupRepos {
				err := testClient.Create(context.Background(), &repo)
				require.NoError(t, err)
			}

			body := createAdmissionReview(tc.repoName, tc.namespace, tc.gitURL, tc.directory, tc.branch)
			request, err := http.NewRequest(http.MethodPost, repositoryValidationEndpoint, bytes.NewReader(body))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			validateRepository(response, request, testClient)

			if tc.expectOK {
				require.Contains(t, response.Body.String(), "Repository validated successfully")
			} else {
				require.Contains(t, response.Body.String(), "Repository conflict")
			}
		})
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "http://172.18.255.200:3000/porch/myrepo.git",
			expected: "http---172.18.255.200-3000-porch-myrepo.git",
		},
		{
			input:    "https://github.com/org/repo.git",
			expected: "https---github.com-org-repo.git",
		},
		{
			input:    "ssh://git@host.com:2222/repo.git",
			expected: "ssh---git@host.com-2222-repo.git",
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := normalizeURL(tc.input)
			require.Equal(t, tc.expected, actual)
		})
	}
}

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
	}

	for _, tc := range tests {
		t.Run(tc.a+"_"+tc.b, func(t *testing.T) {
			actual := isNestedConflict(tc.a, tc.b)
			require.Equal(t, tc.expected, actual)
		})
	}
}

func TestIsConflict(t *testing.T) {
	makeRepo := func(name, ns, url, dir string) *configapi.Repository {
		return &configapi.Repository{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: configapi.RepositorySpec{
				Git: &configapi.GitRepository{
					Repo:      url,
					Directory: dir,
				},
			},
		}
	}

	tests := []struct {
		name     string
		existing *configapi.Repository
		attempt  *configapi.Repository
		expected bool
	}{
		{
			name:     "same url and dir in same namespace",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "dir"),
			expected: true,
		},
		{
			name:     "same url and dir in different namespace",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir"),
			attempt:  makeRepo("repo2", "ns2", "http://host/repo.git", "dir"),
			expected: false,
		},
		{
			name:     "root vs non-root conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", ""),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "dir/sub"),
			expected: true,
		},
		{
			name:     "non-root vs root conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir/sub"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", ""),
			expected: true,
		},
		{
			name:     "nested conflict",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "base"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "base/sub"),
			expected: true,
		},
		{
			name:     "no conflict different url",
			existing: makeRepo("repo1", "ns1", "http://host/repo1.git", "dir"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo2.git", "dir"),
			expected: false,
		},
		{
			name:     "no conflict subdirectories",
			existing: makeRepo("repo1", "ns1", "http://host/repo.git", "dir/sub/sub1"),
			attempt:  makeRepo("repo2", "ns1", "http://host/repo.git", "dir/sub/sub2"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := isConflict(tc.existing, tc.attempt)
			require.Equal(t, tc.expected, actual)
		})
	}
}
