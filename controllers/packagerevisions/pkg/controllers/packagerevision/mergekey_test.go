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

package packagerevision

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func TestEnsureMergeKeyAddsComment(t *testing.T) {
	input := map[string]string{
		"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: my-cm\n  namespace: default\n",
	}

	result, err := ensureMergeKey(input)
	require.NoError(t, err)
	assert.Contains(t, result["cm.yaml"], "kpt-merge: default/my-cm")
}

func TestEnsureMergeKeyPreservesExisting(t *testing.T) {
	input := map[string]string{
		"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: my-cm\n  namespace: default # kpt-merge: default/my-cm\n",
	}

	result, err := ensureMergeKey(input)
	require.NoError(t, err)
	assert.Contains(t, result["cm.yaml"], "kpt-merge: default/my-cm")
}

func TestEnsureMergeKeyNonYamlPassthrough(t *testing.T) {
	input := map[string]string{
		"README.md":  "# hello",
		"config.txt": "some config",
		"cm.yaml":    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n",
	}

	result, err := ensureMergeKey(input)
	require.NoError(t, err)
	assert.Equal(t, "# hello", result["README.md"])
	assert.Equal(t, "some config", result["config.txt"])
	assert.Contains(t, result, "cm.yaml")
}

func TestEnsureMergeKeyEmptyInput(t *testing.T) {
	result, err := ensureMergeKey(map[string]string{})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestEnsureMergeKeyKptfile(t *testing.T) {
	input := map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
	}

	result, err := ensureMergeKey(input)
	require.NoError(t, err)
	assert.Contains(t, result, "Kptfile")
}

func TestEnsureMergeKeyMultipleResources(t *testing.T) {
	input := map[string]string{
		"a.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: ns1\n",
		"b.yml":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n  namespace: ns2\n",
	}

	result, err := ensureMergeKey(input)
	require.NoError(t, err)
	assert.Contains(t, result["a.yaml"], "kpt-merge: ns1/a")
	assert.Contains(t, result["b.yml"], "kpt-merge: ns2/b")
}

func TestEnsureMergeKeyInvalidYaml(t *testing.T) {
	input := map[string]string{
		"bad.yaml": "not: valid: yaml: [",
	}

	_, err := ensureMergeKey(input)
	assert.Error(t, err)
}

func TestMapReaderSeparatesNonYaml(t *testing.T) {
	r := &mapReader{
		input: map[string]string{
			"a.yaml":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n",
			"b.json":  `{"key": "value"}`,
			"c.txt":   "plain text",
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n",
		},
		extra: map[string]string{},
	}

	nodes, err := r.Read()
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, `{"key": "value"}`, r.extra["b.json"])
	assert.Equal(t, "plain text", r.extra["c.txt"])
}

func TestNodePathAnnotationFallback(t *testing.T) {
	node, err := yaml.Parse("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n  namespace: myns\n")
	require.NoError(t, err)
	assert.Equal(t, "myns/test.yaml", nodePathAnnotation(node))
}

func TestNodePathAnnotationNoNamespace(t *testing.T) {
	node, err := yaml.Parse("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n")
	require.NoError(t, err)
	assert.Equal(t, "non-namespaced/test.yaml", nodePathAnnotation(node))
}

func TestNodePathAnnotationNoName(t *testing.T) {
	node, err := yaml.Parse("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  namespace: myns\n")
	require.NoError(t, err)
	assert.Equal(t, "myns/unnamed.yaml", nodePathAnnotation(node))
}
