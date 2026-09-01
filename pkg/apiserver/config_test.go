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

package apiserver

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sampleopenapi "github.com/kptdev/porch/api/generated/openapi"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func completedConfigForTest(t *testing.T, extra ExtraConfig) CompletedConfig {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	return (&Config{
		GenericConfig: &genericapiserver.RecommendedConfig{
			Config: genericapiserver.Config{
				SecureServing: &genericapiserver.SecureServingInfo{
					Listener: ln,
				},
			},
		},
		ExtraConfig: extra,
	}).Complete()
}

func completedConfigForNewTest(t *testing.T, extra ExtraConfig) CompletedConfig {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	serverConfig := genericapiserver.NewRecommendedConfig(Codecs)
	serverConfig.SecureServing = &genericapiserver.SecureServingInfo{Listener: ln}
	serverConfig.LoopbackClientConfig = &rest.Config{Host: "https://127.0.0.1:1"}
	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(Scheme))
	serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(Scheme))

	completed := (&Config{
		GenericConfig: serverConfig,
		ExtraConfig:   extra,
	}).Complete()
	return completed
}

func restConfigWithFakeAPIServer(t *testing.T) *rest.Config {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
	})
	mux.HandleFunc("/apis", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"kind":"APIGroupList","groups":[{"name":"config.porch.kpt.dev","versions":[{"groupVersion":"config.porch.kpt.dev/v1alpha1","version":"v1alpha1"}]}]}`))
	})
	mux.HandleFunc("/apis/config.porch.kpt.dev/v1alpha1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"kind":"APIResourceList",
			"apiVersion":"config.porch.kpt.dev/v1alpha1",
			"resources":[
				{"name":"repositories","singularName":"repository","namespaced":true,"kind":"Repository","verbs":["get","list","watch"]},
				{"name":"functionconfigs","singularName":"functionconfig","namespaced":true,"kind":"FunctionConfig","verbs":["get","list","watch"]}
			]
		}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

func TestBuildManager(t *testing.T) {
	scheme, err := buildCompleteScheme()
	require.NoError(t, err)

	restConfig := restConfigWithFakeAPIServer(t)

	t.Run("without repository index", func(t *testing.T) {
		completed := completedConfigForTest(t, ExtraConfig{})
		mgr, err := completed.buildManager(restConfig, scheme, false)
		require.NoError(t, err)
		assert.NotNil(t, mgr)
	})

	t.Run("with repository index and probe port", func(t *testing.T) {
		completed := completedConfigForTest(t, ExtraConfig{
			ProbePort: 4453,
			HAOptions: HAConfig{
				LeaseDuration: 15 * time.Second,
			},
		})
		completed.deps.newManager = func(cfg *rest.Config, opts ctrl.Options) (manager.Manager, error) {
			opts.HealthProbeBindAddress = "0"
			return ctrl.NewManager(cfg, opts)
		}

		mgr, err := completed.buildManager(restConfig, scheme, true)
		require.NoError(t, err)
		assert.NotNil(t, mgr)
	})
}

func TestRegisterFunctionConfigController(t *testing.T) {
	scheme, err := buildCompleteScheme()
	require.NoError(t, err)

	restConfig := restConfigWithFakeAPIServer(t)
	completed := completedConfigForTest(t, ExtraConfig{})

	mgr, err := completed.buildManager(restConfig, scheme, false)
	require.NoError(t, err)

	err = completed.registerFunctionConfigController(mgr)
	require.NoError(t, err)
	assert.NotNil(t, completed.ExtraConfig.FunctionStore)
}

func TestFunctionConfigStoreUsesFunctionCacheDir(t *testing.T) {
	completed := completedConfigForTest(t, ExtraConfig{
		ExecEvaluatorOptions: engine.ExecutableEvaluatorOptions{
			FunctionCacheDir: "/home/nonroot/functions",
		},
	})

	store := functionconfigs.NewFunctionConfigStore(
		completed.ExtraConfig.GRPCRuntimeOptions.DefaultImagePrefix,
		completed.ExtraConfig.ExecEvaluatorOptions.FunctionCacheDir,
	)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-annotations"},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-annotations",
			Prefixes: []string{""},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{"v0.1.5"},
				Path: "set-annotations",
			},
		},
	}
	store.UpdateBinaryCache(obj.Name, obj)

	path, found := store.GetBinaryFromCache("set-annotations:v0.1.5")
	require.True(t, found)
	assert.Equal(t, "/home/nonroot/functions/set-annotations", path)
}

func TestLeaderElectionID(t *testing.T) {
	assert.Equal(t, "porch-server", LeaderElectionID)
}

// TestGitRepoIndexingFunction tests the git.repo index function used in buildManager
func TestGitRepoIndexingFunction(t *testing.T) {
	indexFunc := func(o client.Object) []string {
		repository := o.(*configapi.Repository)
		if repository.Spec.Git == nil || repository.Spec.Git.Repo == "" {
			return nil
		}
		return []string{repository.Spec.Git.Repo}
	}

	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected []string
	}{
		{
			name: "git repository with valid URL",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo1"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Repo: "http://gitea.local/org/repo.git",
					},
				},
			},
			expected: []string{"http://gitea.local/org/repo.git"},
		},
		{
			name: "OCI repository (nil Git)",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo-oci"},
				Spec: configapi.RepositorySpec{
					Type: configapi.RepositoryTypeOCI,
				},
			},
			expected: nil,
		},
		{
			name: "git repository with empty URL",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo2"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{Repo: ""},
				},
			},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := indexFunc(tc.repo)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestGitBranchIndexingFunction tests the git.branch index function used in buildManager
func TestGitBranchIndexingFunction(t *testing.T) {
	indexFunc := func(o client.Object) []string {
		repository := o.(*configapi.Repository)
		if repository.Spec.Git == nil || repository.Spec.Git.Branch == "" {
			return nil
		}
		return []string{repository.Spec.Git.Branch}
	}

	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected []string
	}{
		{
			name: "repository with main branch",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo1"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Branch: "main",
					},
				},
			},
			expected: []string{"main"},
		},
		{
			name: "repository with develop branch",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo2"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Branch: "develop",
					},
				},
			},
			expected: []string{"develop"},
		},
		{
			name: "OCI repository (nil Git)",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo-oci"},
				Spec:       configapi.RepositorySpec{},
			},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := indexFunc(tc.repo)
			assert.Equal(t, tc.expected, result)
		})
	}
}
