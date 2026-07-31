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
	"os"
	"path/filepath"
	"testing"

	"github.com/kptdev/porch/controllers/functionconfigs"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockengine "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func TestBuildManagerPassesProbePort(t *testing.T) {
	scheme, err := buildCompleteScheme()
	require.NoError(t, err)

	var gotOpts ctrl.Options
	completed := completedConfigForTest(t, ExtraConfig{ProbePort: 4453})
	completed.deps.newManager = func(cfg *rest.Config, opts ctrl.Options) (manager.Manager, error) {
		gotOpts = opts
		opts.HealthProbeBindAddress = "0"
		return ctrl.NewManager(cfg, opts)
	}

	mgr, err := completed.buildManager(restConfigWithFakeAPIServer(t), scheme, false)
	require.NoError(t, err)
	assert.NotNil(t, mgr)
	assert.Equal(t, ":4453", gotOpts.HealthProbeBindAddress)
	assert.Equal(t, LeaderElectionID, gotOpts.LeaderElectionID)
}

func TestNewWithInjectedDeps(t *testing.T) {
	restCfg := restConfigWithFakeAPIServer(t)
	kubeconfig := writeTempKubeconfig(t, restCfg.Host)

	completed := completedConfigForNewTest(t, ExtraConfig{
		CoreAPIKubeconfigPath: kubeconfig,
		HAOptions:             HAConfig{LeaderElection: true},
		CacheOptions:          cachetypes.CacheOptions{CacheType: cachetypes.CRCacheType},
	})

	fakeCache := mockcachetypes.NewMockCache(t)
	fakeEngine := mockengine.NewMockCaDEngine(t)

	completed.deps.getCache = func(ctx context.Context, opts cachetypes.CacheOptions) (cachetypes.Cache, error) {
		assert.NotNil(t, opts.CoreClient)
		return fakeCache, nil
	}
	completed.deps.newEngine = func(opts ...engine.EngineOption) (engine.CaDEngine, error) {
		return fakeEngine, nil
	}
	completed.deps.registerFCController = func(mgr manager.Manager) error {
		completed.ExtraConfig.FunctionStore = functionconfigs.NewFunctionConfigStore("prefix/", "")
		return nil
	}
	completed.deps.registerRCController = func(manager.Manager) error {
		return nil
	}
	completed.deps.newManager = func(cfg *rest.Config, opts ctrl.Options) (manager.Manager, error) {
		// Avoid in-cluster leader-election namespace requirement in unit tests.
		opts.LeaderElection = false
		opts.HealthProbeBindAddress = "0"
		return ctrl.NewManager(cfg, opts)
	}
	completed.deps.cacheRetry = wait.Backoff{Steps: 1}

	mgr, server, err := completed.New(context.Background())
	require.NoError(t, err)
	require.NotNil(t, mgr)
	require.NotNil(t, server)
	assert.True(t, server.NeedLeaderElection())
	assert.Same(t, fakeCache, server.cache)
}

func TestNewManagerError(t *testing.T) {
	restCfg := restConfigWithFakeAPIServer(t)
	kubeconfig := writeTempKubeconfig(t, restCfg.Host)

	completed := completedConfigForNewTest(t, ExtraConfig{
		CoreAPIKubeconfigPath: kubeconfig,
	})
	completed.deps.newManager = func(cfg *rest.Config, opts ctrl.Options) (manager.Manager, error) {
		return nil, fmt.Errorf("manager boom")
	}

	_, server, err := completed.New(context.Background())
	assert.Nil(t, server)
	assert.ErrorContains(t, err, "failed to build controller-runtime manager")
	assert.ErrorContains(t, err, "manager boom")
}

func TestNewCacheError(t *testing.T) {
	restCfg := restConfigWithFakeAPIServer(t)
	kubeconfig := writeTempKubeconfig(t, restCfg.Host)

	completed := completedConfigForNewTest(t, ExtraConfig{
		CoreAPIKubeconfigPath: kubeconfig,
	})
	completed.deps.registerFCController = func(mgr manager.Manager) error {
		completed.ExtraConfig.FunctionStore = functionconfigs.NewFunctionConfigStore("prefix/", "")
		return nil
	}
	completed.deps.registerRCController = func(manager.Manager) error {
		return nil
	}
	completed.deps.getCache = func(ctx context.Context, opts cachetypes.CacheOptions) (cachetypes.Cache, error) {
		return nil, fmt.Errorf("cache boom")
	}
	completed.deps.cacheRetry = wait.Backoff{Steps: 1}

	_, server, err := completed.New(context.Background())
	assert.Nil(t, server)
	assert.ErrorContains(t, err, "failed to create repository cache")
	assert.ErrorContains(t, err, "cache boom")
}

func TestNewEngineError(t *testing.T) {
	restCfg := restConfigWithFakeAPIServer(t)
	kubeconfig := writeTempKubeconfig(t, restCfg.Host)

	completed := completedConfigForNewTest(t, ExtraConfig{
		CoreAPIKubeconfigPath: kubeconfig,
	})
	completed.deps.registerFCController = func(mgr manager.Manager) error {
		completed.ExtraConfig.FunctionStore = functionconfigs.NewFunctionConfigStore("prefix/", "")
		return nil
	}
	completed.deps.registerRCController = func(manager.Manager) error {
		return nil
	}
	completed.deps.getCache = func(ctx context.Context, opts cachetypes.CacheOptions) (cachetypes.Cache, error) {
		return mockcachetypes.NewMockCache(t), nil
	}
	completed.deps.newEngine = func(opts ...engine.EngineOption) (engine.CaDEngine, error) {
		return nil, fmt.Errorf("engine boom")
	}
	completed.deps.cacheRetry = wait.Backoff{Steps: 1}

	_, server, err := completed.New(context.Background())
	assert.Nil(t, server)
	assert.ErrorContains(t, err, "engine boom")
}

func writeTempKubeconfig(t *testing.T, server string) string {
	t.Helper()
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user: {}
`, server)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
