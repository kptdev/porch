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
	"maps"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (pr *PackageRevision) IsPublished() bool {
	return LifecycleIsPublished(pr.Spec.Lifecycle)
}

func LifecycleIsPublished(lifecycle PackageRevisionLifecycle) bool {
	return lifecycle == PackageRevisionLifecyclePublished || lifecycle == PackageRevisionLifecycleDeletionProposed
}

func LifecycleIsDraft(lifecycle PackageRevisionLifecycle) bool {
	return lifecycle == PackageRevisionLifecycleDraft || lifecycle == PackageRevisionLifecycleProposed
}

// PackageRevisionIsReady checks if the package has met all readiness gates
func PackageRevisionIsReady(readinessGates []ReadinessGate, packageConditions []PackageCondition) bool {
	// Index our package conditions
	conds := make(map[string]PackageCondition)
	for _, c := range packageConditions {
		conds[c.Type] = c
	}

	// Check if the readiness gates are met
	for _, g := range readinessGates {
		if _, ok := conds[g.ConditionType]; !ok {
			return false
		}
		if conds[g.ConditionType].Status != PackageConditionTrue {
			return false
		}
	}

	return true
}

// IsPackageCreation checks if the package revision is an init or clone operation
func IsPackageCreation(pkgRev *PackageRevision) bool {
	if pkgRev.Spec.Source == nil {
		return false
	}
	return pkgRev.Spec.Source.Init != nil || pkgRev.Spec.Source.CloneFrom != nil
}

// PackageMetadataEqual returns true if two PackageMetadata values have identical labels and annotations.
func PackageMetadataEqual(a, b *PackageMetadata) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return maps.Equal(a.Labels, b.Labels) && maps.Equal(a.Annotations, b.Annotations)
}

// TimesEqual returns true if two metav1.Time pointers have equal values.
// Two nil pointers are equal; a nil and a non-nil pointer are not.
func TimesEqual(a, b *metav1.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(b)
}
