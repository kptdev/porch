// Copyright 2022, 2024-2025 The kpt Authors
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

package repository

import (
	"context"
	"fmt"

	"github.com/kptdev/kpt/pkg/lib/builtins/builtintypes"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/objects"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type PackageFetcher struct {
	RepoOpener        RepositoryOpener
	ReferenceResolver ReferenceResolver
}

func (p *PackageFetcher) FetchRevision(ctx context.Context, packageRevisionRef *porchapi.PackageRevisionRef, namespace string) (PackageRevision, error) {
	prKey, err := PkgRevK8sName2Key(namespace, packageRevisionRef.Name)
	if err != nil {
		return nil, err
	}

	var resolved configapi.Repository
	if err := p.ReferenceResolver.ResolveReference(ctx, namespace, prKey.RKey().Name, &resolved); err != nil {
		return nil, fmt.Errorf("cannot find repository %+v: %w", prKey.RKey(), err)
	}

	repo, err := p.RepoOpener.OpenRepository(ctx, &resolved)
	if err != nil {
		return nil, err
	}

	revisions, err := repo.ListPackageRevisions(ctx, ListPackageRevisionFilter{Key: prKey})
	if err != nil {
		return nil, err
	}

	if len(revisions) != 1 {
		return nil, fmt.Errorf("cannot find package revision %q", packageRevisionRef.Name)
	}

	return revisions[0], nil
}

func (p *PackageFetcher) FetchResources(ctx context.Context, packageRevisionRef *porchapi.PackageRevisionRef, namespace string) (*porchapi.PackageRevisionResources, error) {
	revision, err := p.FetchRevision(ctx, packageRevisionRef, namespace)
	if err != nil {
		return nil, err
	}

	resources, err := revision.GetResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot read contents of package %q: %w", packageRevisionRef.Name, err)
	}
	return resources, nil
}

func BuildPackageConfig(ctx context.Context, obj *porchapi.PackageRevision, parent PackageRevision) (*builtintypes.PackageConfig, error) {
	config := &builtintypes.PackageConfig{}

	parentPath := ""

	var parentConfig *unstructured.Unstructured
	if parent != nil {
		parentObj, err := parent.GetPackageRevision(ctx)
		if err != nil {
			return nil, err
		}
		parentPath = parentObj.Spec.PackageName

		resources, err := parent.GetResources(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting resources from parent package %q: %w", parentObj.Name, err)
		}
		configMapObj, err := extractContextConfigMap(resources.Spec.Resources)
		if err != nil {
			return nil, fmt.Errorf("error getting configuration from parent package %q: %w", parentObj.Name, err)
		}
		parentConfig = configMapObj

		if parentConfig != nil {
			// TODO: Should we support kinds other than configmaps?
			var parentConfigMap corev1.ConfigMap
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(parentConfig.Object, &parentConfigMap); err != nil {
				return nil, fmt.Errorf("error parsing ConfigMap from parent configuration: %w", err)
			}
			if s := parentConfigMap.Data[builtintypes.ConfigKeyPackagePath]; s != "" {
				parentPath = s + "/" + parentPath
			}
		}
	}

	if parentPath == "" {
		config.PackagePath = obj.Spec.PackageName
	} else {
		config.PackagePath = parentPath + "/" + obj.Spec.PackageName
	}

	return config, nil
}

// ExtractContextConfigMap returns the package-context configmap, if found
func extractContextConfigMap(resources map[string]string) (*unstructured.Unstructured, error) {
	unstructureds, err := objects.Parser{}.AsUnstructureds(resources)
	if err != nil {
		return nil, err
	}

	var matches []*unstructured.Unstructured
	for _, o := range unstructureds {
		configMapGK := schema.GroupKind{Kind: "ConfigMap"}
		if o.GroupVersionKind().GroupKind() == configMapGK {
			if o.GetName() == builtintypes.PkgContextName {
				matches = append(matches, o)
			}
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}

	if len(matches) > 1 {
		return nil, fmt.Errorf("found multiple configmaps matching name %q", builtintypes.PkgContextFile)
	}

	return matches[0], nil
}
