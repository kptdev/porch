// Copyright 2023 The kpt Authors
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
	"fmt"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
)

const (
	ResourceVersionAnnotation = "internal.kpt.dev/resource-version"
)

func GetResourceFileKubeObject(prr *porchapi.PackageRevisionResources, file, kind, name string) (*fnsdk.KubeObject, error) {
	if prr.Spec.Resources == nil {
		return nil, fmt.Errorf("nil resources found for PackageRevisionResources '%s/%s'", prr.Namespace, prr.Name)
	}

	if _, ok := prr.Spec.Resources[file]; !ok {
		return nil, fmt.Errorf("%q not found in PackageRevisionResources '%s/%s'", file, prr.Namespace, prr.Name)
	}

	ko, err := fnsdk.ParseKubeObject([]byte(prr.Spec.Resources[file]))
	if err != nil {
		return nil, fmt.Errorf("failed to parse %q of PackageRevisionResources %s/%s: %w", file, prr.Namespace, prr.Name, err)
	}
	if kind != "" && ko.GetKind() != kind {
		return nil, fmt.Errorf("%q does not contain kind %q in PackageRevisionResources '%s/%s'", file, kind, prr.Namespace, prr.Name)
	}
	if name != "" && ko.GetName() != name {
		return nil, fmt.Errorf("%q does not contain resource named %q in PackageRevisionResources '%s/%s'", file, name, prr.Namespace, prr.Name)
	}

	return ko, nil
}

func GetResourceVersion(prr *porchapi.PackageRevisionResources) (string, error) {
	ko, err := GetResourceFileKubeObject(prr, kptfilev1.RevisionMetaDataFileName, kptfilev1.RevisionMetaDataKind, "")
	if err != nil {
		return "", err
	}
	rv, _, _ := ko.NestedString("metadata", "resourceVersion")
	return rv, nil
}

func AddRevisionMetadata(prr *porchapi.PackageRevisionResources) error {
	kptMetaDataKo := fnsdk.NewEmptyKubeObject()
	if err := kptMetaDataKo.SetAPIVersion(prr.APIVersion); err != nil {
		return fmt.Errorf("cannot set Api Version: %v", err)
	}
	if err := kptMetaDataKo.SetKind(kptfilev1.RevisionMetaDataKind); err != nil {
		return fmt.Errorf("cannot set Kind: %v", err)
	}
	if err := kptMetaDataKo.SetNestedField(prr.GetObjectMeta(), "metadata"); err != nil {
		return fmt.Errorf("cannot set metadata: %v", err)
	}
	prr.Spec.Resources[kptfilev1.RevisionMetaDataFileName] = kptMetaDataKo.String()

	return nil
}

func RemoveRevisionMetadata(prr *porchapi.PackageRevisionResources) error {
	delete(prr.Spec.Resources, kptfilev1.RevisionMetaDataFileName)
	return nil
}
