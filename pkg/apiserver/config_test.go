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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
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

func TestLeaderElectionID(t *testing.T) {
	assert.Equal(t, "porch-server", LeaderElectionID)
}
