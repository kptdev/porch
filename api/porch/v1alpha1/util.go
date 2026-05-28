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

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Valid relative paths should not start with '/', should not contain '..' components,
// and should only contain valid path characters
var validRelativePathRegex = regexp.MustCompile(`^(?:[a-zA-Z0-9._-]+(?:/[a-zA-Z0-9._-]+)*)?$`)

// Check that there are no '..' components in a path
var noDoubleDots = regexp.MustCompile(`(^|/)\.\.(/|$)`)

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
func GetSubpackageDir(pkgRev *PackageRevision) (string, error) {
	if len(pkgRev.Spec.Tasks) == 0 {
		return "", fmt.Errorf("failed to get subpackage directory, task list must have at least one entry")
	}

	if len(pkgRev.Spec.Tasks) > 2 {
		return "", fmt.Errorf("failed to get subpackage directory, task list may not have more than two entries")
	}

	if getSubpackageDir(pkgRev.Spec.Tasks[0]) != "" {
		return "", fmt.Errorf("subpackage directory may not be specified as the first task on the task list")
	}

	if len(pkgRev.Spec.Tasks) < 2 {
		return "", nil
	}

	subpackageDir := getSubpackageDir(pkgRev.Spec.Tasks[1])
	if IsValidSubpackageDir(subpackageDir) {
		return subpackageDir, nil
	} else {
		return "", fmt.Errorf("subpackage directory %q is invalid", subpackageDir)
	}
}

// getSubpackageDir gets the SubpackageDir from a task or returns "" if it does not exist
func getSubpackageDir(task Task) string {
	switch task.Type {
	case TaskTypeClone:
		if task.Clone == nil {
			return ""
		}
		return task.Clone.SubpackageDir
	case TaskTypeUpgrade:
		if task.Upgrade == nil {
			return ""
		}
		return task.Upgrade.SubpackageDir
	default:
		return ""
	}
}

// IsValidSubpackageDir returns true if subpackageDir is valid, false otherwise.
func IsValidSubpackageDir(subpackageDir string) bool {
	// Empty string is invalid, a subpackage directory must be a relative path.
	if subpackageDir == "" {
		return false
	}

	// Check basic format and ensure it doesn't contain '..' or start with '/' or end with '/'
	if subpackageDir[0] == '/' || strings.HasSuffix(subpackageDir, "/") || noDoubleDots.MatchString(subpackageDir) {
		return false
	}

	// Reject any path segment equal to "." (for example ".", "./subpkg", or "subpkg/./nested").
	for _, segment := range strings.Split(subpackageDir, "/") {
		if segment == "." {
			return false
		}
	}

	return validRelativePathRegex.MatchString(subpackageDir)
}

func (pr *PackageRevision) IsPushOnRenderFailure() bool {
	ann := pr.GetAnnotations()
	v, ok := ann[PushOnFnRenderFailureKey]
	return ok && v == PushOnFnRenderFailureValue
}
