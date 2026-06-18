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

	pkgerrors "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

// validRelativePathRegex validates the basic shape of a relative path (slash-separated segments made of allowed characters).
// Additional constraints (e.g. no leading/trailing '/', no '.', and DNS1123-compliant name composition) are enforced in IsValidSubpackageDir.
var validRelativePathRegex = regexp.MustCompile(`^(?:[a-zA-Z0-9._-]+(?:/[a-zA-Z0-9._-]+)*)?$`)

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
	if err := IsValidSubpackageDir(subpackageDir); err == nil {
		return subpackageDir, nil
	} else {
		return "", err
	}
}

// IsValidSubpackageDir returns an error if subpackageDir is invalid.
func IsValidSubpackageDir(subpackageDir string) error {
	// Empty string is invalid, a subpackage directory must be a relative path.
	if subpackageDir == "" {
		return pkgerrors.Errorf("subpackage directory %q is invalid", subpackageDir)
	}

	// Check basic format and ensure it doesn't start with '/', doesn't end with '/', and doesn't contain '.'
	if subpackageDir[0] == '/' || strings.HasSuffix(subpackageDir, "/") || strings.Contains(subpackageDir, ".") {
		return pkgerrors.Errorf("subpackage directory %q is invalid, it cannot contain '.' or start with '/' or end with '/'", subpackageDir)
	}

	if !validRelativePathRegex.MatchString(subpackageDir) {
		return pkgerrors.Errorf("subpackage directory %q is invalid, it must match regular expression %q", subpackageDir, validRelativePathRegex.String())
	}

	if _, err := ComposeSubpkgObjName(subpackageDir); err != nil {
		return err
	}

	return nil
}

func ComposeSubpkgObjName(subpackageDir string) (string, error) {
	if subpackageDir == "" {
		return "", pkgerrors.Errorf("subpackage directory %q is invalid", subpackageDir)
	}

	subpackageName := strings.ReplaceAll(subpackageDir, "/", ".")

	objNameErrs := validation.IsDNS1123Subdomain(subpackageName)

	if len(objNameErrs) == 0 {
		return subpackageName, nil
	} else {
		return "", pkgerrors.Errorf("subpackage resource name %q invalid: %s", subpackageName, strings.Join(objNameErrs, ","))
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

func (pr *PackageRevision) IsPushOnRenderFailure() bool {
	ann := pr.GetAnnotations()
	v, ok := ann[PushOnFnRenderFailureKey]
	return ok && v == PushOnFnRenderFailureValue
}
