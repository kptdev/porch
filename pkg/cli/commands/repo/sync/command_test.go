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

package sync

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/lib/util/cmdutil"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/cmd/get"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Shared helper to create a runner with flags and optional client
func setupTestRunner(flags map[string]string, namespace string, client client.Client) *runner {
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	for name, value := range flags {
		cmd.Flags().String(name, value, "")
	}

	return &runner{
		ctx:     context.Background(),
		Command: cmd,
		getFlags: cmdutil.Options{
			ConfigFlags: &genericclioptions.ConfigFlags{Namespace: strPtr(namespace)},
		},
		printFlags: &get.PrintFlags{
			HumanReadableFlags: &get.HumanPrintFlags{},
		},
		client: client,
	}
}

func strPtr(s string) *string {
	return &s
}

func TestRunE_VariousRunOnceScenarios(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	// Create multiple repo objects
	repo1 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Sync: &configapi.RepositorySync{},
		},
	}
	repo2 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Sync: &configapi.RepositorySync{},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo1, repo2).Build()

	tests := []struct {
		name          string
		flags         map[string]string
		args          []string
		namespace     string
		expectError   bool
		errorContains string
		expectRunOnce bool
		expectedRunAt string
		expectedRepos []string
	}{
		{
			name: "Invalid run-once format",
			flags: map[string]string{
				"run-once":       "invalid",
				"all":            "false",
				"all-namespaces": "false",
			},
			args:          []string{"repo1"},
			namespace:     "default",
			expectError:   true,
			errorContains: "invalid --run-once value",
		},
		{
			name: "Repository does not exist",
			flags: map[string]string{
				"run-once":       "2025-09-25T09:00:00Z",
				"all":            "false",
				"all-namespaces": "false",
			},
			args:          []string{"nonexistent-repo"},
			namespace:     "default",
			expectError:   true,
			errorContains: "no repositories found in namespace \"default\"",
		},
		{
			name: "Missing namespace",
			flags: map[string]string{
				"run-once":       "2025-09-25T09:00:00Z",
				"all":            "true",
				"all-namespaces": "false",
			},
			args:          []string{},
			namespace:     "", // simulate missing namespace
			expectError:   true,
			errorContains: "namespace must be specified",
		},
		{
			name: "Namespace does not exist",
			flags: map[string]string{
				"run-once":       "2025-09-25T09:00:00Z",
				"all":            "true",
				"all-namespaces": "false",
			},
			args:          []string{},
			namespace:     "ghost", // simulate non-existent namespace
			expectError:   true,
			errorContains: "no repositories found in namespace \"ghost\"",
		},
		{
			name: "Missing repo name",
			flags: map[string]string{
				"run-once":       "",
				"all":            "false",
				"all-namespaces": "false",
			},
			args:          []string{},
			namespace:     "default",
			expectError:   true,
			errorContains: "repository name(s) required unless --all is set",
		},
		{
			name: "Successful sync with duration for one repo",
			flags: map[string]string{
				"run-once":       "2m",
				"all":            "false",
				"all-namespaces": "false",
			},
			args:          []string{"repo1"},
			namespace:     "default",
			expectError:   false,
			expectRunOnce: true,
			expectedRepos: []string{"repo1"},
		},
		{
			name: "Successful sync with duration less than 1 minute for one repo",
			flags: map[string]string{
				"run-once":       "10s",
				"all":            "false",
				"all-namespaces": "false",
			},
			args:          []string{"repo1"},
			namespace:     "default",
			expectError:   false,
			expectRunOnce: true,
			expectedRepos: []string{"repo1"},
		},
		{
			name: "Successful sync with RFC3339 time for multiple repos",
			flags: map[string]string{
				"run-once":       time.Now().Add(5 * time.Minute).Format(time.RFC3339),
				"all":            "false",
				"all-namespaces": "false",
			},
			args:          []string{"repo1", "repo2"},
			namespace:     "default",
			expectError:   false,
			expectRunOnce: true,
			expectedRunAt: time.Now().Add(5 * time.Minute).Format(time.RFC3339),
			expectedRepos: []string{"repo1", "repo2"},
		},
		{
			name: "Sync all repos with RFC3339 time using --all",
			flags: map[string]string{
				"run-once":       time.Now().Add(5 * time.Minute).Format(time.RFC3339),
				"all":            "true",
				"all-namespaces": "false",
			},
			args:          []string{},
			namespace:     "default",
			expectError:   false,
			expectRunOnce: true,
			expectedRunAt: time.Now().Add(5 * time.Minute).Format(time.RFC3339),
			expectedRepos: []string{"repo1", "repo2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := setupTestRunner(tt.flags, tt.namespace, fakeClient)
			err := r.runE(r.Command, tt.args)

			if tt.expectError {
				if err == nil || (tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains)) {
					t.Errorf("expected error containing '%s', got %v", tt.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if tt.expectRunOnce {
				for _, repoName := range tt.expectedRepos {
					updated := &configapi.Repository{}
					err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: repoName}, updated)
					if err != nil {
						t.Fatalf("failed to get updated repo %s: %v", repoName, err)
					}

					if updated.Spec.Sync == nil || updated.Spec.Sync.RunOnceAt == nil {
						t.Fatalf("expected RunOnceAt to be set for repo %s", repoName)
					}

					if tt.expectedRunAt != "" {
						expectedTime, err := time.Parse(time.RFC3339, tt.expectedRunAt)
						if err != nil {
							t.Fatalf("failed to parse expected time: %v", err)
						}
						if !updated.Spec.Sync.RunOnceAt.Time.Equal(expectedTime) {
							t.Errorf("expected RunOnceAt for repo %s to be %v, got %v", repoName, expectedTime, updated.Spec.Sync.RunOnceAt.Time)
						}
					}
				}
			}
		})
	}
}

func TestRunE_NsScoped(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	// Repos in different namespaces
	repoDefault1 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{Sync: &configapi.RepositorySync{}},
	}
	repoDefault2 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{Sync: &configapi.RepositorySync{}},
	}
	repoOther := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo3",
			Namespace: "other",
		},
		Spec: configapi.RepositorySpec{Sync: &configapi.RepositorySync{}},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repoDefault1, repoDefault2, repoOther).Build()

	tests := []struct {
		name            string
		flags           map[string]string
		args            []string
		expectError     bool
		errorContains   string
		expectRunOnce   bool
		expectedRunAt   string
		expectedRepos   []types.NamespacedName
		unexpectedRepos []types.NamespacedName
	}{
		{
			name: "Sync all repos in default namespace only",
			flags: map[string]string{
				"run-once":       time.Now().Add(5 * time.Minute).Format(time.RFC3339),
				"all":            "true",
				"all-namespaces": "false",
			},
			args:            []string{},
			expectError:     false,
			expectRunOnce:   true,
			expectedRunAt:   time.Now().Add(5 * time.Minute).Format(time.RFC3339),
			expectedRepos:   []types.NamespacedName{{Name: "repo1", Namespace: "default"}, {Name: "repo2", Namespace: "default"}},
			unexpectedRepos: []types.NamespacedName{{Name: "repo3", Namespace: "other"}},
		},
		{
			name: "Sync all repos in all namespaces",
			flags: map[string]string{
				"run-once":       time.Now().Add(5 * time.Minute).Format(time.RFC3339),
				"all":            "true",
				"all-namespaces": "true",
			},
			args:          []string{},
			expectError:   false,
			expectRunOnce: true,
			expectedRunAt: time.Now().Add(5 * time.Minute).Format(time.RFC3339),
			expectedRepos: []types.NamespacedName{{Name: "repo1", Namespace: "default"}, {Name: "repo2", Namespace: "default"}, {Name: "repo3", Namespace: "other"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := setupTestRunner(tt.flags, "default", fakeClient)
			err := r.runE(r.Command, tt.args)

			if tt.expectError {
				if err == nil || (tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains)) {
					t.Errorf("expected error containing '%s', got %v", tt.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if tt.expectRunOnce {
				expectedTime, err := time.Parse(time.RFC3339, tt.expectedRunAt)
				if err != nil {
					t.Fatalf("failed to parse expected time: %v", err)
				}

				for _, repoKey := range tt.expectedRepos {
					updated := &configapi.Repository{}
					err := fakeClient.Get(context.Background(), repoKey, updated)
					if err != nil {
						t.Fatalf("failed to get updated repo %s/%s: %v", repoKey.Namespace, repoKey.Name, err)
					}
					if updated.Spec.Sync == nil || updated.Spec.Sync.RunOnceAt == nil {
						t.Fatalf("expected RunOnceAt to be set for repo %s/%s", repoKey.Namespace, repoKey.Name)
					}
					if !updated.Spec.Sync.RunOnceAt.Time.Equal(expectedTime) {
						t.Errorf("expected RunOnceAt for repo %s/%s to be %v, got %v", repoKey.Namespace, repoKey.Name, expectedTime, updated.Spec.Sync.RunOnceAt.Time)
					}
				}

				for _, repoKey := range tt.unexpectedRepos {
					updated := &configapi.Repository{}
					err := fakeClient.Get(context.Background(), repoKey, updated)
					if err != nil {
						t.Fatalf("failed to get repo %s/%s: %v", repoKey.Namespace, repoKey.Name, err)
					}
					if updated.Spec.Sync != nil && updated.Spec.Sync.RunOnceAt != nil {
						t.Errorf("expected RunOnceAt to be unset for repo %s/%s, got %v", repoKey.Namespace, repoKey.Name, updated.Spec.Sync.RunOnceAt.Time)
					}
				}
			}
		})
	}
}

func TestRunE_MixedSyncStates(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	// repo1 has no Sync
	repo1 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: "default",
		},
	}

	// repo2 has Sync but no RunOnceAt
	repo2 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Sync: &configapi.RepositorySync{},
		},
	}

	// repo3 has Sync and RunOnceAt already set
	existingTime := metav1.NewTime(time.Date(2025, 9, 1, 12, 0, 0, 0, time.UTC))
	repo3 := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo3",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Sync: &configapi.RepositorySync{
				RunOnceAt: &existingTime,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo1, repo2, repo3).Build()

	runOnceTime := time.Now().Add(5 * time.Minute)
	flags := map[string]string{
		"run-once":       runOnceTime.Format(time.RFC3339),
		"all":            "true",
		"all-namespaces": "false",
	}
	r := setupTestRunner(flags, "default", fakeClient)

	err := r.runE(r.Command, []string{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedTime, _ := time.Parse(time.RFC3339, runOnceTime.Format(time.RFC3339))
	repoNames := []string{"repo1", "repo2", "repo3"}

	for _, name := range repoNames {
		updated := &configapi.Repository{}
		err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, updated)
		if err != nil {
			t.Fatalf("failed to get updated repo %s: %v", name, err)
		}
		if updated.Spec.Sync == nil || updated.Spec.Sync.RunOnceAt == nil {
			t.Errorf("expected RunOnceAt to be set for repo %s", name)
			continue
		}
		if !updated.Spec.Sync.RunOnceAt.Time.Equal(expectedTime) {
			t.Errorf("expected RunOnceAt for repo %s to be %v, got %v", name, expectedTime, updated.Spec.Sync.RunOnceAt.Time)
		}
	}
}

func TestNewRunnerInitialization(t *testing.T) {
	ctx := context.Background()
	configFlags := &genericclioptions.ConfigFlags{Namespace: strPtr("default")}

	r := newRunner(ctx, configFlags)

	if r == nil {
		t.Fatal("expected runner to be initialized, got nil")
	}
	if r.Command == nil {
		t.Fatal("expected Command to be initialized")
	}
	if r.getFlags.ConfigFlags != configFlags {
		t.Errorf("expected ConfigFlags to be set correctly")
	}
	if r.Command.Use != "sync [REPOSITORY_NAME]" {
		t.Errorf("unexpected command use: %s", r.Command.Use)
	}
	if !r.Command.Flags().Lookup("all").Changed {
		// Just checking the flag exists
		if r.Command.Flags().Lookup("all") == nil {
			t.Errorf("expected --all flag to be present")
		}
	}
	if r.Command.Flags().Lookup("run-once") == nil {
		t.Errorf("expected --run-once flag to be present")
	}
}

func TestRunE_ClientCreationFailure(t *testing.T) {
	// Test the scenario where porch.CreateClientWithFlags fails
	r := &runner{
		ctx: context.Background(),
		getFlags: cmdutil.Options{
			ConfigFlags: &genericclioptions.ConfigFlags{
				KubeConfig: strPtr("/invalid/path/to/kubeconfig"),
				Namespace:  strPtr("default"),
			},
		},
		client: nil, // Force client creation
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("all", "false", "")
	cmd.Flags().String("all-namespaces", "false", "")
	cmd.Flags().String("run-once", "", "")
	r.Command = cmd

	err := r.runE(cmd, []string{"test-repo"})
	assert.Error(t, err, "expected error when client creation fails")
}
