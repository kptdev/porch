// Copyright 2022, 2025 The kpt Authors
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

package porch

import (
	"fmt"
	"slices"
	"strings"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/selection"
)

type prFilterFieldMappingFunc func(filter *repository.ListPackageRevisionFilter, value string) error

var (
	PrFilterFieldMappings = map[porchapi.PkgRevFieldSelector]prFilterFieldMappingFunc{
		porchapi.PkgRevSelectorName: func(f *repository.ListPackageRevisionFilter, name string) error {
			var err error
			if filterKey, err := repository.PkgRevK8sName2Key("", name); err == nil {
				f.Key = filterKey
			}
			return err
		},
		porchapi.PkgRevSelectorNamespace: func(f *repository.ListPackageRevisionFilter, namespace string) error {
			f.Key.PkgKey.RepoKey.Namespace = namespace
			return nil
		},
		porchapi.PkgRevSelectorRevision: func(f *repository.ListPackageRevisionFilter, strRevision string) error {
			f.Key.Revision = repository.Revision2Int(strRevision)
			return nil
		},
		porchapi.PkgRevSelectorPackageName: func(f *repository.ListPackageRevisionFilter, fullPkgName string) error {
			split := strings.Split(fullPkgName, "/")
			if len(split) > 1 {
				f.Key.PkgKey.Package = split[len(split)-1]
				f.Key.PkgKey.Path = strings.Join(split[0:len(split)-1], "/")
			} else {
				f.Key.PkgKey.Package = fullPkgName
			}
			return nil
		},
		porchapi.PkgRevSelectorRepository: func(f *repository.ListPackageRevisionFilter, repoName string) error {
			f.Key.PkgKey.RepoKey.Name = repoName
			return nil
		},
		porchapi.PkgRevSelectorWorkspaceName: func(f *repository.ListPackageRevisionFilter, workspaceName string) error {
			f.Key.WorkspaceName = workspaceName
			return nil
		},
		porchapi.PkgRevSelectorLifecycle: func(f *repository.ListPackageRevisionFilter, lifecycle string) error {
			var err error
			l := porchapi.PackageRevisionLifecycle(lifecycle)
			if l.IsValid() {
				f.Lifecycles = append(f.Lifecycles, l)
			} else {
				err = apierrors.NewBadRequest(fmt.Sprintf("unsupported fieldSelector value %q for field %q", lifecycle, porchapi.PkgRevSelectorLifecycle))
			}
			return err
		},
	}
)

// convertPackageFieldSelector is the schema conversion function for normalizing the FieldSelector for Package
func convertPackageFieldSelector(label, value string) (internalLabel, internalValue string, err error) {
	switch label {
	case "metadata.name":
		return label, value, nil
	case "metadata.namespace":
		return label, value, nil
	case "spec.packageName", "spec.repository":
		return label, value, nil
	default:
		return "", "", fmt.Errorf("%q is not a known field selector", label)
	}
}

// convertPackageRevisionFieldSelector is the schema conversion function for normalizing the FieldSelector for PackageRevision
func convertPackageRevisionFieldSelector(label, value string) (internalLabel, internalValue string, err error) {
	if slices.Contains(porchapi.PackageRevisionSelectableFields, porchapi.PkgRevFieldSelector(label)) {
		return label, value, nil
	}

	if strings.HasPrefix(label, "spec.packageMetadata.labels[") && strings.HasSuffix(label, "]") {
		start := len("spec.packageMetadata.labels[")
		end := len(label) - 1
		labelKey := label[start:end]
		if labelKey == "" {
			return "", "", fmt.Errorf("label key cannot be empty in field selector %q", label)
		}

		return label, value, nil
	}

	return "", "", fmt.Errorf("%q is not a known field selector", label)
}

// parsePackageFieldSelector parses client-provided fields.Selector into a packageFilter.
// If namespace is non-empty, it is applied to the filter and checked for conflicts
// with any namespace specified via the field selector.
func parsePackageFieldSelector(fieldSelector fields.Selector, namespace string) (repository.ListPackageFilter, error) {
	var filter repository.ListPackageFilter

	if fieldSelector == nil {
		filter.Key.RepoKey.Namespace = namespace
		return filter, nil
	}

	requirements := fieldSelector.Requirements()
	for _, requirement := range requirements {

		switch requirement.Operator {
		case selection.Equals, selection.DoesNotExist:
			if requirement.Value == "" {
				return filter, apierrors.NewBadRequest(fmt.Sprintf("unsupported fieldSelector value %q for field %q with operator %q", requirement.Value, requirement.Field, requirement.Operator))
			}
		default:
			return filter, apierrors.NewBadRequest(fmt.Sprintf("unsupported fieldSelector operator %q for field %q", requirement.Operator, requirement.Field))
		}

		switch requirement.Field {
		case "metadata.name":
			if filterKey, err := repository.PkgK8sName2Key("", requirement.Value); err == nil {
				filter.Key = filterKey
			} else {
				return repository.ListPackageFilter{}, err
			}

		case "metadata.namespace":
			filter.Key.RepoKey.Namespace = requirement.Value

		case "spec.packageName":
			filter.Key.Package = requirement.Value

		case "spec.repository":
			filter.Key.RepoKey.Name = requirement.Value

		default:
			return filter, apierrors.NewBadRequest(fmt.Sprintf("unknown fieldSelector field %q", requirement.Field))
		}
	}

	if namespace != "" {
		if filter.Key.RepoKey.Namespace != "" && filter.Key.RepoKey.Namespace != namespace {
			return filter, fmt.Errorf("conflicting namespaces specified: %q and %q", namespace, filter.Key.RepoKey.Namespace)
		}
		filter.Key.RepoKey.Namespace = namespace
	}

	return filter, nil
}

// parsePackageRevisionFieldSelector parses client-provided fields.Selector into a ListPackageRevisionFilter.
// If namespace is non-empty, it is applied to the filter and checked for conflicts
// with any namespace specified via the field selector.
func parsePackageRevisionFieldSelector(options *metainternalversion.ListOptions, namespace string) (*repository.ListPackageRevisionFilter, error) {
	filter := &repository.ListPackageRevisionFilter{
		Label: options.LabelSelector,
		Key:   repository.PackageRevisionKey{},
	}

	fieldSelector := options.FieldSelector
	if fieldSelector == nil {
		filter.Key.PkgKey.RepoKey.Namespace = namespace
		return filter, nil
	}

	for _, requirement := range fieldSelector.Requirements() {
		switch requirement.Operator {
		case selection.Equals, selection.DoubleEquals:
			if requirement.Value == "" {
				return filter, apierrors.NewBadRequest(fmt.Sprintf("unsupported fieldSelector value %q for field %q with operator %q", requirement.Value, requirement.Field, requirement.Operator))
			}
		default:
			return filter, apierrors.NewBadRequest(fmt.Sprintf("unsupported fieldSelector operator %q for field %q", requirement.Operator, requirement.Field))
		}

		if strings.HasPrefix(requirement.Field, "spec.packageMetadata.labels[") && strings.HasSuffix(requirement.Field, "]") {
			start := len("spec.packageMetadata.labels[")
			end := len(requirement.Field) - 1
			labelKey := requirement.Field[start:end]

			if labelKey == "" {
				return filter, apierrors.NewBadRequest(fmt.Sprintf("label key cannot be empty in field selector %q", requirement.Field))
			}

			if filter.KptfileLabels == nil {
				filter.KptfileLabels = make(map[string]string)
			}

			filter.KptfileLabels[labelKey] = requirement.Value

			// skip the upcoming requirement.Field check
			continue
		}

		filteredField := porchapi.PkgRevFieldSelector(requirement.Field)
		if filterFunc, fieldSelectable := PrFilterFieldMappings[filteredField]; fieldSelectable {
			if err := filterFunc(filter, requirement.Value); err != nil {
				return filter, err
			}
		} else {
			return filter, apierrors.NewBadRequest(fmt.Sprintf("unknown fieldSelector field %q", requirement.Field))
		}
	}

	if namespace != "" {
		if filter.Key.PkgKey.RepoKey.Namespace != "" && filter.Key.PkgKey.RepoKey.Namespace != namespace {
			return filter, fmt.Errorf("conflicting namespaces specified: %q and %q", namespace, filter.Key.PkgKey.RepoKey.Namespace)
		}
		filter.Key.PkgKey.RepoKey.Namespace = namespace
	}

	return filter, nil
}

// parsePackageRevisionResourcesFieldSelector parses client-provided fields.Selector into a packageRevisionFilter
func parsePackageRevisionResourcesFieldSelector(options *metainternalversion.ListOptions, namespace string) (*repository.ListPackageRevisionFilter, error) {
	// TOOD: This is a little weird, because we don't have the same fields on PackageRevisionResources.
	// But we probably should have the key fields
	return parsePackageRevisionFieldSelector(options, namespace)
}
