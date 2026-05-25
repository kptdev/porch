// Copyright 2022-2024, 2026 The kpt Authors
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
// limitations under the License

package v1alpha1

import "slices"

func (pr *PackageRevision) IsPublished() bool {
	return LifecycleIsPublished(pr.Spec.Lifecycle)
}

func LifecycleIsPublished(lifecycle PackageRevisionLifecycle) bool {
	return lifecycle == PackageRevisionLifecyclePublished || lifecycle == PackageRevisionLifecycleDeletionProposed
}

func (l *PackageRevisionLifecycle) IsValid() bool {
	switch *l {
	case PackageRevisionLifecycleDraft,
		PackageRevisionLifecycleProposed,
		PackageRevisionLifecyclePublished,
		PackageRevisionLifecycleDeletionProposed:
		return true
	default:
		return false
	}
}

// Check ReadinessGates checks if the package has met all readiness gates
func PackageRevisionIsReady(readinessGates []ReadinessGate, conditions []Condition) bool {
	// Index our conditions
	conds := make(map[string]Condition)
	for _, c := range conditions {
		conds[c.Type] = c
	}

	// Check if the readiness gates are met
	for _, g := range readinessGates {
		if _, ok := conds[g.ConditionType]; !ok {
			return false
		}
		if conds[g.ConditionType].Status != "True" {
			return false
		}
	}

	return true
}

var validFirstTaskTypes = []TaskType{TaskTypeInit, TaskTypeEdit, TaskTypeClone, TaskTypeUpgrade}

func IsValidFirstTaskType(t TaskType) bool {
	return slices.Contains(validFirstTaskTypes, t)
}

// IsPackageCreation checks if the package revision is an init or clone operation
func IsPackageCreation(pkgRev *PackageRevision) bool {
	for _, task := range pkgRev.Spec.Tasks {
		if task.Type == TaskTypeInit || task.Type == TaskTypeClone {
			return true
		}
	}
	return false
}

// GetSubpackageDir returns the SubpackageDir for a package revision,
// or "" if there is no SubpackageDir set.
func GetSubpackageDir(pkgRev *PackageRevision) string {
	for _, task := range pkgRev.Spec.Tasks {
		if task.Type == TaskTypeClone {
			if task.Clone == nil || task.Clone.SubpackageDir == "" {
				continue
			}
			return task.Clone.SubpackageDir
		}
		if task.Type == TaskTypeUpgrade {
			if task.Upgrade == nil || task.Upgrade.SubpackageDir == "" {
				continue
			}
			return task.Upgrade.SubpackageDir
		}
	}
	return ""
}

func (pr *PackageRevision) IsPushOnRenderFailure() bool {
	ann := pr.GetAnnotations()
	v, ok := ann[PushOnFnRenderFailureKey]
	return ok && v == PushOnFnRenderFailureValue
}
