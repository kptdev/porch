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

package util

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

const invalidConst string = " is invalid: "

// Revision2Int parses a revision such as "3" or "v3".
// It returns -1 when the value is not an integer; -1 is the main revision.
func Revision2Int(revisionStr string) int {
	revisionStr = strings.TrimPrefix(revisionStr, "v")

	if revision, err := strconv.Atoi(revisionStr); err == nil {
		return revision
	} else {
		return -1
	}
}

// Revision2Int64 parses a revision such as "3" or "v3".
// It returns -1 when the value is not an integer; -1 is the main revision.
func Revision2Int64(revision string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimPrefix(revision, "v"), 10, 64)
	if err != nil {
		return -1
	}
	return parsed
}

// Revision2Str formats a revision number without a "v" prefix.
func Revision2Str[T int | int64](revision T) string {
	return strconv.FormatInt(int64(revision), 10)
}

// ValidateK8SName checks that k8sName is an RFC 1123 DNS label.
func ValidateK8SName(k8sName string) error {
	if k8sNameErrs := validation.IsDNS1123Label(k8sName); k8sNameErrs != nil {
		return errors.New(strings.Join(k8sNameErrs, ","))
	}

	return nil
}

// ValidateDirectoryName checks that directory is an RFC 1123 DNS label
// with '/' allowed as a separator. When mandatory is false, an empty
// directory (including "/") is valid.
func ValidateDirectoryName(directory string, mandatory bool) error {
	var dirErrs []string
	if strings.Contains(directory, "//") {
		dirErrs = append(dirErrs, "consecutive '/' characters are not allowed")
	}
	dirNoSlash := strings.ReplaceAll(directory, "/", "")
	if mandatory || len(dirNoSlash) > 0 {
		dirErrs = append(dirErrs, validation.IsDNS1123Label(dirNoSlash)...)
	} else {
		// The directory is "/"
		dirErrs = nil
	}

	if dirErrs == nil {
		return nil
	} else {
		return errors.New(strings.Join(dirErrs, ","))
	}
}

// ValidateRepository checks that repoName is an RFC 1123 DNS label and
// that directory is a valid repository directory. An empty directory is allowed.
func ValidateRepository(repoName, directory string) error {
	nameErrs := validation.IsDNS1123Label(repoName)

	dirErr := ValidateDirectoryName(directory, false)

	if nameErrs == nil && dirErr == nil {
		return nil
	}

	repoErrString := ""

	if nameErrs != nil {
		repoErrString = "repository name " + repoName + invalidConst + strings.Join(nameErrs, ",") + "\n"
	}

	dirErrString := ""
	if dirErr != nil {
		dirErrString = "directory name " + directory + invalidConst + dirErr.Error() + "\n"
	}

	return errors.New(repoErrString + dirErrString)
}

// ComposePkgObjName builds a Package object name, repo.path.package, with
// '/' in path replaced by '.'. It returns "" when repoName or packageName is empty.
func ComposePkgObjName(repoName, path, packageName string) string {
	if len(repoName) == 0 || len(packageName) == 0 {
		return ""
	}

	dottedPath := strings.ReplaceAll(filepath.Join(path, packageName), "/", ".")
	dottedPath = strings.Trim(dottedPath, ".")
	return fmt.Sprintf("%s.%s", repoName, dottedPath)
}

// ComposePkgRevObjName builds a PackageRevision object name,
// repo.path.package.workspace. It returns "" when repoName, packageName,
// or workspace is empty. The revision number is not included.
func ComposePkgRevObjName(repoName, path, packageName, workspace string) string {
	if len(repoName) == 0 || len(packageName) == 0 || len(workspace) == 0 {
		return ""
	}
	dottedPath := strings.ReplaceAll(filepath.Join(path, packageName), "/", ".")
	dottedPath = strings.Trim(dottedPath, ".")
	return fmt.Sprintf("%s.%s.%s", repoName, dottedPath, workspace)
}

// ValidPkgObjName checks that the composed Package object name is a DNS-1123 subdomain.
func ValidPkgObjName(repoName, path, packageName string) error {
	errSlice := validPkgNamePart(repoName, path, packageName)

	if len(errSlice) == 0 {
		objName := ComposePkgObjName(repoName, path, packageName)

		if objNameErrs := validation.IsDNS1123Subdomain(objName); objNameErrs != nil {
			errSlice = append(errSlice, fmt.Sprintf("package kubernetes name %q is invalid\n", objName))
			errSlice = append(errSlice, "package kubernetes name "+objName+invalidConst+strings.Join(objNameErrs, "")+"\n")
		}
	}

	if len(errSlice) == 0 {
		return nil
	} else {
		return errors.New("package kubernetes resource name is invalid:\n" + strings.Join(errSlice, ""))
	}
}

// ValidPkgRevObjName checks that workspace is a DNS-1123 label and that the
// composed PackageRevision object name is a DNS-1123 subdomain.
func ValidPkgRevObjName(repoName, path, packageName, workspace string) error {
	errSlice := validPkgNamePart(repoName, path, packageName)

	if err := ValidateK8SName(workspace); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("workspace name part %q of package revision name is invalid\n", workspace))
		errSlice = append(errSlice, "workspace name "+workspace+invalidConst+err.Error()+"\n")
	}

	if len(errSlice) == 0 {
		objName := ComposePkgRevObjName(repoName, path, packageName, workspace)

		if objNameErrs := validation.IsDNS1123Subdomain(objName); objNameErrs != nil {
			errSlice = append(errSlice, fmt.Sprintf("package revision kubernetes name %q is invalid\n", objName))
			errSlice = append(errSlice, "package revision kubernetes name "+objName+invalidConst+strings.Join(objNameErrs, "")+"\n")
		}
	}

	if len(errSlice) == 0 {
		return nil
	} else {
		return errors.New("package revision kubernetes resource name is invalid:\n" + strings.Join(errSlice, ""))
	}
}

func validPkgNamePart(repoName, path, packageName string) []string {
	var errSlice []string

	if err := ValidateRepository(repoName, ""); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("repository part %q of object name is invalid\n", repoName))
		errSlice = append(errSlice, err.Error())
	}

	if err := ValidateDirectoryName(path, false); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("package path part %q of object name is invalid\n", path))
		errSlice = append(errSlice, "path "+path+invalidConst+err.Error()+"\n")
	}

	if err := ValidateK8SName(packageName); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("package name part %q of object name is invalid\n", packageName))
		errSlice = append(errSlice, "package name "+packageName+invalidConst+err.Error()+"\n")
	}

	return errSlice
}

// GetPRWorkspaceName returns the workspace suffix of a PackageRevision object name.
// A trailing semver (".v1.2.3") is kept intact, including its dots; otherwise
// the suffix is the segment after the last dot. It returns "" when k8sName has no '.'.
func GetPRWorkspaceName(k8sName string) string {
	if !strings.Contains(k8sName, ".") {
		return ""
	}

	if semverFound, _ := regexp.MatchString("\\.v[0-9\\.]*[0-9]$", k8sName); semverFound {
		return k8sName[strings.LastIndex(k8sName, ".v")+1:]
	} else {
		return k8sName[strings.LastIndex(k8sName, ".")+1:]
	}
}

// SplitIn3OnDelimiter splits splitee into exactly three parts: head, middle, and tail.
// One segment yields [s, "", ""]; two yield [first, "", last]. Further segments
// are rejoined into middle, so a dotted package path stays in the middle.
func SplitIn3OnDelimiter(splitee, delimiter string) []string {
	splitSlice := make([]string, 3)

	split := strings.Split(splitee, delimiter)

	switch len(split) {
	case 0:
		return splitSlice
	case 1:
		splitSlice[0] = split[0]
		return splitSlice
	case 2:
		splitSlice[0] = split[0]
		splitSlice[2] = split[1]
		return splitSlice
	case 3:
		splitSlice[0] = split[0]
		splitSlice[1] = split[1]
		splitSlice[2] = split[2]
		return splitSlice
	}

	splitSlice[0] = split[0]
	splitSlice[1] = split[1]
	splitSlice[2] = split[len(split)-1]

	for i := 2; i < len(split)-1; i++ {
		splitSlice[1] = splitSlice[1] + delimiter + split[i]
	}

	return splitSlice
}
