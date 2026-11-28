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

package v1alpha2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// An empty path is a whole-package edge. It is omitted from JSON, and a read without
// it leaves Path empty.
func TestUpstreamDependentEmptyPathIsOmitted(t *testing.T) {
	out, err := json.Marshal(UpstreamDependent{Name: "custom-a"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"name":"custom-a"}` {
		t.Fatalf("unexpected JSON %s", out)
	}

	var in UpstreamDependent
	if err := json.Unmarshal([]byte(`{"name":"custom-a"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Path != "" {
		t.Fatalf("path = %q, want empty", in.Path)
	}
}

func TestUpstreamDependentsRoundTrip(t *testing.T) {
	obj := &UpstreamDependents{
		Spec: UpstreamDependentsSpec{
			UpstreamKey:     "vendor.v1",
			UpstreamPackage: "repo.vendor.pkg",
		},
		Status: UpstreamDependentsStatus{
			Dependents: []UpstreamDependent{
				{Name: "custom-a"},
				{Name: "custom-a", Path: "network"},
			},
			DependentCount: 2,
		},
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	var got UpstreamDependents
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec != obj.Spec || got.Status.DependentCount != 2 || len(got.Status.Dependents) != 2 {
		t.Fatalf("round trip changed the object: %+v", got)
	}
}

// DeepCopy must not share the dependents slice with the original.
func TestUpstreamDependentsDeepCopyIsIndependent(t *testing.T) {
	obj := &UpstreamDependents{
		Status: UpstreamDependentsStatus{Dependents: []UpstreamDependent{{Name: "custom-a"}}},
	}
	cp := obj.DeepCopy()
	cp.Status.Dependents[0].Name = "changed"

	if obj.Status.Dependents[0].Name != "custom-a" {
		t.Fatal("DeepCopy shared the dependents slice")
	}
}

// TestUpstreamDependentsCRDSelectableFields verifies that the generated UpstreamDependents CRD
// can be listed by upstream, so a field selector on spec.upstreamKey or spec.upstreamPackage
// returns the dependents of one upstream.
func TestUpstreamDependentsCRDSelectableFields(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(crdsDir(), "porch.kpt.dev_upstreamdependents.yaml"))
	require.NoError(t, err, "failed to read CRD YAML — run 'make generate' first")

	var crd CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd), "failed to parse CRD YAML")

	var version *CustomResourceDefinitionVersion
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == "v1alpha2" {
			version = &crd.Spec.Versions[i]
			break
		}
	}
	require.NotNil(t, version, "v1alpha2 version not found in CRD")

	paths := make(map[string]bool)
	for _, sf := range version.SelectableFields {
		paths[sf.JSONPath] = true
	}
	assert.Equal(t, map[string]bool{
		".spec.upstreamKey":     true,
		".spec.upstreamPackage": true,
	}, paths)
}
