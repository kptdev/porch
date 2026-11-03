// Copyright 2022 The kpt Authors
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

// +domain=kpt.dev

package apis

//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0 object:headerFile="../scripts/boilerplate.go.txt",year=$YEAR_GEN crd:crdVersions=v1,headerFile="../scripts/boilerplate.yaml.txt",year=$YEAR_GEN output:crd:artifacts:config=./generated/crds paths={./porchconfig/...,./porch/v1alpha2/...}
// we use v0.21.0 because `json:",inline"` just does not work with newer versions
// caused by https://github.com/kubernetes-sigs/controller-tools/pull/1449
// also, since the output package is the same for all 3 paths, the last one always overwrites the other two's internal/* and utils.go
//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 applyconfiguration:headerFile="../scripts/boilerplate.go.txt",externalApplyConfigurations=github.com/kptdev/kpt/api/kptfile/v1.Function@github.com/kptdev/kpt/api/generated/applyconfigurations/kptfile/v1;github.com/kptdev/kpt/api/kptfile/v1.Selector@github.com/kptdev/kpt/api/generated/applyconfigurations/kptfile/v1;github.com/kptdev/kpt/api/kptfile/v1.ResourceReference@github.com/kptdev/kpt/api/generated/applyconfigurations/kptfile/v1 paths={./porchconfig/...,./porch/...}
