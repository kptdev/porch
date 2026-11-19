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
	generated "github.com/kptdev/porch/api/generated/openapi"
	"k8s.io/kube-openapi/pkg/common"
)

// GetOpenAPIDefinitions returns the merged OpenAPI definitions for the Porch API server.
func GetOpenAPIDefinitions(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
	defs := generated.GetOpenAPIDefinitions(ref)
	addKptfileOpenAPIDefinitions(ref, defs)
	return defs
}
