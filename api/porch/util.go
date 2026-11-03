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

package porch

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// validRelativePathRegex validates the basic shape of a relative path (slash-separated segments made of allowed characters).
// Additional constraints (e.g. no leading/trailing '/', no '.', and DNS1123-compliant name composition) are enforced in IsValidSubpackageDir.
var validRelativePathRegex = regexp.MustCompile(`^(?:[a-zA-Z0-9._-]+(?:/[a-zA-Z0-9._-]+)*)?$`)

// IsValidSubpackageDir returns an error if subpackageDir is invalid.
func IsValidSubpackageDir(subpackageDir string) error {
	// Empty string is invalid, a subpackage directory must be a relative path.
	if subpackageDir == "" {
		return fmt.Errorf("subpackage directory %q is invalid", subpackageDir)
	}

	// Check basic format and ensure it doesn't start with '/', doesn't end with '/', and doesn't contain '.'
	if subpackageDir[0] == '/' || strings.HasSuffix(subpackageDir, "/") || strings.Contains(subpackageDir, ".") {
		return fmt.Errorf("subpackage directory %q is invalid, it cannot contain '.' or start with '/' or end with '/'", subpackageDir)
	}

	if !validRelativePathRegex.MatchString(subpackageDir) {
		return fmt.Errorf("subpackage directory %q is invalid, it must match regular expression %q", subpackageDir, validRelativePathRegex.String())
	}

	if _, err := ComposeSubpkgObjName(subpackageDir); err != nil {
		return err
	}

	return nil
}

func ComposeSubpkgObjName(subpackageDir string) (string, error) {
	if subpackageDir == "" {
		return "", fmt.Errorf("subpackage directory %q is invalid", subpackageDir)
	}

	subpackageName := strings.ReplaceAll(subpackageDir, "/", ".")

	objNameErrs := validation.IsDNS1123Subdomain(subpackageName)

	if len(objNameErrs) == 0 {
		return subpackageName, nil
	} else {
		return "", fmt.Errorf("subpackage resource name %q invalid: %s", subpackageName, strings.Join(objNameErrs, ","))
	}
}
