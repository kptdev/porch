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

package util

import (
	"os"
	"testing"
)

// WriteTempKubeconfig writes a minimal kubeconfig pointing at an unreachable
// host into t.TempDir() and returns the path. It is sufficient to exercise
// cfg.ToRESTConfig() and a lazy client.New() without requiring a live cluster,
// and is intended for use by rpkg sub-command preRunE tests that need a real
// configuration path on disk.
func WriteTempKubeconfig(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/kubeconfig"
	const kubeconfigYAML = `apiVersion: v1
kind: Config
clusters:
- cluster: {server: https://127.0.0.1:1}
  name: t
contexts:
- context: {cluster: t, user: t}
  name: t
current-context: t
users:
- name: t
`
	if err := os.WriteFile(path, []byte(kubeconfigYAML), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}
