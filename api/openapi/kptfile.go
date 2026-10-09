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

package openapi

import (
	"k8s.io/kube-openapi/pkg/common"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const (
	kptfileRenderStatus       = "github.com/kptdev/kpt/api/kptfile/v1.RenderStatus"
	kptfilePipelineStepResult = "github.com/kptdev/kpt/api/kptfile/v1.PipelineStepResult"
	fnresultResultItem        = "github.com/kptdev/kpt/api/fnresult/v1.ResultItem"
	fnresultField             = "github.com/kptdev/kpt/api/fnresult/v1.Field"
	frameworkFile             = "sigs.k8s.io/kustomize/kyaml/fn/framework.File"
	yamlResourceIdentifier    = "sigs.k8s.io/kustomize/kyaml/yaml.ResourceIdentifier"
)

func addKptfileOpenAPIDefinitions(ref common.ReferenceCallback, defs map[string]common.OpenAPIDefinition) {
	defs[kptfileRenderStatus] = schemaKptfileRenderStatus(ref)
	defs[kptfilePipelineStepResult] = schemaKptfilePipelineStepResult(ref)
	defs[fnresultResultItem] = schemaFnresultResultItem(ref)
	defs[fnresultField] = schemaFnresultField(ref)
	defs[frameworkFile] = schemaFrameworkFile(ref)
	defs[yamlResourceIdentifier] = schemaYamlResourceIdentifier(ref)
}

func schemaKptfileRenderStatus(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "RenderStatus represents the result of performing render operation on a package's resources.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"mutationSteps": {
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Default: map[string]interface{}{},
										Ref:     ref(kptfilePipelineStepResult),
									},
								},
							},
						},
					},
					"validationSteps": {
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Default: map[string]interface{}{},
										Ref:     ref(kptfilePipelineStepResult),
									},
								},
							},
						},
					},
					"errorSummary": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
				},
			},
		},
		Dependencies: []string{kptfilePipelineStepResult},
	}
}

func schemaKptfilePipelineStepResult(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "PipelineStepResult contains the structured result from an individual function call in the pipeline.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"name": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"image": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"exec": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"executionError": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"stderr": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"exitCode": {
						SchemaProps: spec.SchemaProps{
							Default: 0,
							Type:    []string{"integer"},
							Format:  "int32",
						},
					},
					"results": {
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Default: map[string]interface{}{},
										Ref:     ref(fnresultResultItem),
									},
								},
							},
						},
					},
					"errorResults": {
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Default: map[string]interface{}{},
										Ref:     ref(fnresultResultItem),
									},
								},
							},
						},
					},
				},
				Required: []string{"exitCode"},
			},
		},
		Dependencies: []string{fnresultResultItem},
	}
}

func schemaFnresultResultItem(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "ResultItem is a modified version of sigs.k8s.io/kustomize/kyaml/fn/framework.Result with a simplified Field field.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"message": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"severity": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"resourceRef": {
						SchemaProps: spec.SchemaProps{
							Ref: ref(yamlResourceIdentifier),
						},
					},
					"field": {
						SchemaProps: spec.SchemaProps{
							Ref: ref(fnresultField),
						},
					},
					"file": {
						SchemaProps: spec.SchemaProps{
							Ref: ref(frameworkFile),
						},
					},
					"tags": {
						SchemaProps: spec.SchemaProps{
							Type: []string{"object"},
							AdditionalProperties: &spec.SchemaOrBool{
								Allows: true,
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Default: "",
										Type:    []string{"string"},
										Format:  "",
									},
								},
							},
						},
					},
				},
			},
		},
		Dependencies: []string{fnresultField, frameworkFile, yamlResourceIdentifier},
	}
}

func schemaFnresultField(_ common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "Field is a modified version of sigs.k8s.io/kustomize/kyaml/fn/framework.Field where CurrentValue and ProposedValue are strings instead of the original any type.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"path": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"currentValue": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"proposedValue": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
				},
			},
		},
	}
}

func schemaFrameworkFile(_ common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "File references a file containing a resource",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"path": {
						SchemaProps: spec.SchemaProps{
							Description: "Path is relative path to the file containing the resource. This field is required.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"index": {
						SchemaProps: spec.SchemaProps{
							Description: "Index is the index into the file containing the resource (i.e. if there are multiple resources in a single file)",
							Type:        []string{"integer"},
							Format:      "int32",
						},
					},
				},
			},
		},
	}
}

func schemaYamlResourceIdentifier(_ common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "ResourceIdentifier contains the information needed to uniquely identify a resource in a cluster.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion is the apiVersion field of a Resource",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is the kind field of a Resource",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"name": {
						SchemaProps: spec.SchemaProps{
							Description: "Name is the metadata.name field of a Resource",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"namespace": {
						SchemaProps: spec.SchemaProps{
							Description: "Namespace is the metadata.namespace field of a Resource",
							Type:        []string{"string"},
							Format:      "",
						},
					},
				},
			},
		},
	}
}
