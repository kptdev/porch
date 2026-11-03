package v1alpha2

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// TestCRDSelectableFieldsMatchConstants verifies that the generated CRD YAML
// contains selectableFields entries for every non-metadata PkgRevFieldSelector
// constant. This catches drift between the kubebuilder markers on the struct
// and the Go constants used by the controller field indexes and CLI.
func TestCRDSelectableFieldsMatchConstants(t *testing.T) {
	crdPath := filepath.Join(crdsDir(), "porch.kpt.dev_packagerevisions.yaml")
	data, err := os.ReadFile(crdPath)
	require.NoError(t, err, "failed to read CRD YAML — run 'make generate' first")

	var crd CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd), "failed to parse CRD YAML")

	// Find the v1alpha2 version
	var version *CustomResourceDefinitionVersion
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == "v1alpha2" {
			version = &crd.Spec.Versions[i]
			break
		}
	}
	require.NotNil(t, version, "v1alpha2 version not found in CRD")
	require.NotEmpty(t, version.SelectableFields, "selectableFields is empty — kubebuilder markers missing?")

	// Build set of jsonPaths from CRD
	crdFields := make(map[string]bool)
	for _, sf := range version.SelectableFields {
		crdFields[sf.JSONPath] = true
	}

	// Every non-metadata selector constant must have a matching selectableField
	for _, sel := range PackageRevisionSelectableFields {
		if sel == PkgRevSelectorName || sel == PkgRevSelectorNamespace {
			continue // metadata fields are handled natively by k8s
		}
		jsonPath := "." + string(sel) // "spec.lifecycle" -> ".spec.lifecycle"
		assert.True(t, crdFields[jsonPath],
			"PkgRevFieldSelector %q (jsonPath %q) missing from CRD selectableFields", sel, jsonPath)
	}

	// Every selectableField in the CRD must have a matching constant
	for _, sf := range version.SelectableFields {
		// Strip leading dot: ".spec.lifecycle" -> "spec.lifecycle"
		fieldPath := sf.JSONPath[1:]
		found := false
		for _, sel := range PackageRevisionSelectableFields {
			if string(sel) == fieldPath {
				found = true
				break
			}
		}
		assert.True(t, found,
			"CRD selectableField %q has no matching PkgRevFieldSelector constant", sf.JSONPath)
	}
}

// crdsDir returns the directory containing the generated CRD YAML files.
func crdsDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filename, "..", "..", "..", "generated", "crds")
}

// copied from k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1@v0.36.3 with modifications
// so that we don't have to import it
// Copyright 2019 The Kubernetes Authors

type CustomResourceDefinition struct {
	Spec CustomResourceDefinitionSpec `json:"spec"`
}

type CustomResourceDefinitionSpec struct {
	Versions []CustomResourceDefinitionVersion `json:"versions"`
}

type CustomResourceDefinitionVersion struct {
	Name             string            `json:"name"`
	SelectableFields []SelectableField `json:"selectableFields,omitempty"`
}

type SelectableField struct {
	JSONPath string `json:"jsonPath"`
}
