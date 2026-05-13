// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package kind

import (
	"github.com/kptdev/porch/test/disaster/api/environment/shell"
	"github.com/kptdev/porch/test/e2e/suiteutils"
)

const (
	dbCacheCluster = "porch-disaster-test-dbcache"
)

var (
	dbCacheKubeconfigFile     = "/test/disaster/deployment/kubeconfigs/porch_dbcache.conf"
	dataClusterKubeconfigFile = "/test/disaster/deployment/kubeconfigs/data_cluster.conf"
)

func Wipe(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Wiping Porch Kind clusters...")
	if err := deleteCluster(t, dbCacheCluster); err != nil {
		t.Fatalf("error wiping Porch cluster with DB cache: error deleting Kind cluster %q: %w", dbCacheCluster, err)
	}
	t.DropCachedClients(dbCacheKubeconfigFile)

	t.Logf("Wiped Porch Kind clusters")
}

func ReconnectDBCacheCluster(t *suiteutils.MultiClusterTestSuite) {
	t.DropCachedClients(dbCacheKubeconfigFile)
	UseDBCacheCluster(t)
}

func Reinstall(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Reinstalling Porch Kind clusters...")
	if err := createCluster(t, dbCacheCluster, t.PorchRoot+dbCacheKubeconfigFile); err != nil {
		t.Fatalf("error reinstalling Porch cluster %q: error creating Kind cluster: %w", dbCacheCluster, err)
	}

	// recreated cluster has different kubeconfig - re-do client setup
	UseDBCacheCluster(t)
	if err := installPorchDBCache(t); err != nil {
		t.Fatalf("error reinstalling Porch cluster %q: error deploying Porch: %w", dbCacheCluster, err.Error())
	}
}

func UseDataCluster(t *suiteutils.MultiClusterTestSuite) error {
	t.T().Helper()

	t.Logf("Switching kubeconfig to data cluster")
	return t.UseKubeconfigFile(dataClusterKubeconfigFile)
}
func UseDBCacheCluster(t *suiteutils.MultiClusterTestSuite) error {
	t.T().Helper()

	t.Logf("Switching kubeconfig to Porch cluster with DB cache")
	return t.UseKubeconfigFile(dbCacheKubeconfigFile)
}

func deleteCluster(t *suiteutils.MultiClusterTestSuite, clusterName string) error {
	t.T().Helper()

	return (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLine("kind", "delete", "cluster", "--name", clusterName)
}

func createCluster(t *suiteutils.MultiClusterTestSuite, clusterName string, kubeconfigPath string) error {
	t.T().Helper()

	t.Logf("Creating Kind cluster %q", clusterName)
	return (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLine("kind", "create", "cluster", "--name", clusterName, "--kubeconfig", kubeconfigPath)
}

func installPorchDBCache(t *suiteutils.MultiClusterTestSuite) error {
	t.T().Helper()

	t.Logf("Installing Porch with DB cache")
	return (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLine("make",
		"-C", t.PorchRoot, "load-images-to-kind",
		"deploy-current-config", "IMAGE_REPO=porch-kind", "IMAGE_TAG=test", "SKIP_IMG_BUILD=true",
		"KIND_CONTEXT_NAME="+dbCacheCluster, "DEPLOYPORCHCONFIGDIR=\""+t.PorchRoot+"/.build/disaster-test/dbcache\"", "KUBECONFIG="+t.PorchRoot+dbCacheKubeconfigFile)
}
