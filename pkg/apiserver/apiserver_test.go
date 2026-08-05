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
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2/textlogger"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

const k8sServHostEnv = "KUBERNETES_SERVICE_HOST"
const k8sServPortEnv = "KUBERNETES_SERVICE_PORT"
const loopbackAnyPort = "127.0.0.1:0"
const loopbackPort1 = "127.0.0.1:1"

func TestMain(m *testing.M) {
	ctrllog.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))
	os.Exit(m.Run())
}

func TestBuildCompleteScheme(t *testing.T) {
	scheme, err := buildCompleteScheme()
	require.NoError(t, err)
	require.NotNil(t, scheme)

	scheme2, err := buildCompleteScheme()
	require.NoError(t, err)
	assert.Same(t, scheme, scheme2, "expected buildCompleteScheme to return singleton instance")
}

func TestBuildSchemeWithTypes(t *testing.T) {
	tests := []struct {
		name        string
		builders    []schemeBuilder
		expectError bool
	}{
		{
			name: "success with valid builders",
			builders: []schemeBuilder{
				func(s *runtime.Scheme) error {
					return corev1.AddToScheme(s)
				},
			},
			expectError: false,
		},
		{
			name: "error from first builder",
			builders: []schemeBuilder{
				func(s *runtime.Scheme) error {
					return fmt.Errorf("mock error")
				},
			},
			expectError: true,
		},
		{
			name: "error from second builder",
			builders: []schemeBuilder{
				func(s *runtime.Scheme) error {
					return corev1.AddToScheme(s)
				},
				func(s *runtime.Scheme) error {
					return fmt.Errorf("second builder error")
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, err := buildSchemeWithTypes(tt.builders...)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "error")
				assert.Nil(t, scheme)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, scheme)
			}
		})
	}
}

func TestKubeConfig(t *testing.T) {
	kubernetesServiceHost := os.Getenv(k8sServHostEnv)
	kubernetesServicePort := os.Getenv(k8sServPortEnv)
	_ = os.Unsetenv(k8sServHostEnv)
	_ = os.Unsetenv(k8sServPortEnv)

	t.Cleanup(func() {
		_ = os.Setenv(k8sServHostEnv, kubernetesServiceHost)
		_ = os.Setenv(k8sServPortEnv, kubernetesServicePort)
	})

	t.Run("path is empty", func(t *testing.T) {
		ln, err := net.Listen("tcp", loopbackAnyPort)
		require.NoError(t, err)
		defer ln.Close()

		cfg := &Config{
			GenericConfig: &genericapiserver.RecommendedConfig{
				Config: genericapiserver.Config{
					SecureServing: &genericapiserver.SecureServingInfo{
						Listener: ln,
					},
				},
			},
		}

		completed := cfg.Complete()
		assert.NotNil(t, completed.GenericConfig.EffectiveVersion)
		assert.Equal(t, "1.0", completed.GenericConfig.EffectiveVersion.BinaryVersion().String())

		restConfig, err := completed.getRestConfig()
		assert.ErrorContains(t, err, "failed to load in-cluster config (specify --kubeconfig if not running in-cluster):")
		assert.Nil(t, restConfig)
	})

	t.Run("failed to load config", func(t *testing.T) {
		ln, err := net.Listen("tcp", loopbackAnyPort)
		require.NoError(t, err)
		defer ln.Close()

		cfg := &Config{
			GenericConfig: &genericapiserver.RecommendedConfig{
				Config: genericapiserver.Config{
					SecureServing: &genericapiserver.SecureServingInfo{
						Listener: ln,
					},
				},
			},
			ExtraConfig: ExtraConfig{
				CoreAPIKubeconfigPath: "/non/existent/path",
			},
		}
		completed := cfg.Complete()

		restConfig, err := completed.getRestConfig()
		assert.ErrorContains(t, err, "failed to load config")
		assert.Nil(t, restConfig)
	})

	t.Run("successful buildClient execution", func(t *testing.T) {
		ln, err := net.Listen("tcp", loopbackAnyPort)
		require.NoError(t, err)
		defer ln.Close()

		cfg := &Config{
			GenericConfig: &genericapiserver.RecommendedConfig{
				Config: genericapiserver.Config{
					SecureServing: &genericapiserver.SecureServingInfo{
						Listener: ln,
					},
				},
			},
		}
		completed := cfg.Complete()

		restConfig := &rest.Config{Host: "https://" + loopbackPort1}
		scheme, err := buildCompleteScheme()
		require.NoError(t, err)

		clientWithWatch, err := completed.buildClient(restConfig, scheme, nil)
		require.NoError(t, err)
		assert.NotNil(t, clientWithWatch)
	})
}

func TestGetCoreV1Client(t *testing.T) {
	t.Run("getCoreV1Client without any error", func(t *testing.T) {
		ln, err := net.Listen("tcp", loopbackAnyPort)
		require.NoError(t, err)
		defer ln.Close()

		cfg := &Config{
			GenericConfig: &genericapiserver.RecommendedConfig{
				Config: genericapiserver.Config{
					SecureServing: &genericapiserver.SecureServingInfo{
						Listener: ln,
					},
				},
			},
		}
		completed := cfg.Complete()

		restConfig := &rest.Config{Host: "https://" + loopbackPort1}
		corev1client, err := completed.getCoreV1Client(restConfig)
		assert.NotNil(t, corev1client)
		assert.NoError(t, err)
	})
}

func TestNew(t *testing.T) {
	t.Run("uninitialized Serializer", func(t *testing.T) {
		ctx := context.Background()
		ln, err := net.Listen("tcp", loopbackAnyPort)
		require.NoError(t, err)
		defer ln.Close()

		cfg := &Config{
			GenericConfig: &genericapiserver.RecommendedConfig{
				Config: genericapiserver.Config{
					SecureServing: &genericapiserver.SecureServingInfo{
						Listener: ln,
					},
				},
			},
			ExtraConfig: ExtraConfig{
				CoreAPIKubeconfigPath: "/non/existent/path",
			},
		}
		completed := cfg.Complete()

		_, porchServer, err := completed.New(ctx)
		assert.Nil(t, porchServer)
		assert.ErrorContains(t, err, "Genericapiserver.New() called with config.Serializer == nil")
	})
}

func TestZeroToNil(t *testing.T) {
	assert.Nil(t, zeroToNil(0))
	assert.Equal(t, new(time.Second), zeroToNil(time.Second))
}

func TestPorchServerNeedLeaderElection(t *testing.T) {
	s := &PorchServer{leaderElect: true}
	assert.True(t, s.NeedLeaderElection())

	s.leaderElect = false
	assert.False(t, s.NeedLeaderElection())
}
