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

const invalidConst string = " invalid: "

func Revision2Int(revisionStr string) int {
	revisionStr = strings.TrimPrefix(revisionStr, "v")

	if revision, err := strconv.Atoi(revisionStr); err == nil {
		return revision
	} else {
		return -1
	}
}

func Revision2Str(revision int) string {
	return strconv.Itoa(revision)
}

func ValidateK8SName(k8sName string) error {
	if k8sNameErrs := validation.IsDNS1123Label(k8sName); k8sNameErrs != nil {
		return errors.New(strings.Join(k8sNameErrs, ","))
	}

	return nil
}

func ValidateDirectoryName(directory string, mandatory bool) error {
	// A directory must follow the rules for RFC 1123 DNS labels except that we allow '/' characters
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

func ValidateRepository(repoName, directory string) error {
	// The repo name must follow the rules for RFC 1123 DNS labels
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

func ComposePkgObjName(repoName, path, packageName string) string {
	if len(repoName) == 0 || len(packageName) == 0 {
		return ""
	}

	dottedPath := strings.ReplaceAll(filepath.Join(path, packageName), "/", ".")
	dottedPath = strings.Trim(dottedPath, ".")
	return fmt.Sprintf("%s.%s", repoName, dottedPath)
}

func ComposePkgRevObjName(repoName, path, packageName, workspace string) string {
	if len(repoName) == 0 || len(packageName) == 0 || len(workspace) == 0 {
		return ""
	}
	dottedPath := strings.ReplaceAll(filepath.Join(path, packageName), "/", ".")
	dottedPath = strings.Trim(dottedPath, ".")
	return fmt.Sprintf("%s.%s.%s", repoName, dottedPath, workspace)
}

func ValidPkgObjName(repoName, path, packageName string) error {
	errSlice := validPkgNamePart(repoName, path, packageName)

	if len(errSlice) == 0 {
		objName := ComposePkgObjName(repoName, path, packageName)

		if objNameErrs := validation.IsDNS1123Subdomain(objName); objNameErrs != nil {
			errSlice = append(errSlice, fmt.Sprintf("package kubernetes name %q invalid\n", objName))
			errSlice = append(errSlice, "package kubernetes name "+objName+invalidConst+strings.Join(objNameErrs, "")+"\n")
		}
	}

	if len(errSlice) == 0 {
		return nil
	} else {
		return errors.New("package kubernetes resource name invalid:\n" + strings.Join(errSlice, ""))
	}
}

func ValidPkgRevObjName(repoName, path, packageName, workspace string) error {
	errSlice := validPkgNamePart(repoName, path, packageName)

	if err := ValidateK8SName(workspace); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("workspace name part %q of package revision name invalid\n", workspace))
		errSlice = append(errSlice, "workspace name "+workspace+invalidConst+err.Error()+"\n")
	}

	if len(errSlice) == 0 {
		objName := ComposePkgRevObjName(repoName, path, packageName, workspace)

		if objNameErrs := validation.IsDNS1123Subdomain(objName); objNameErrs != nil {
			errSlice = append(errSlice, fmt.Sprintf("package revision kubernetes name %q invalid\n", objName))
			errSlice = append(errSlice, "package revision kubernetes name "+objName+invalidConst+strings.Join(objNameErrs, "")+"\n")
		}
	}

	if len(errSlice) == 0 {
		return nil
	} else {
		return errors.New("package revision kubernetes resource name invalid:\n" + strings.Join(errSlice, ""))
	}
}

func validPkgNamePart(repoName, path, packageName string) []string {
	var errSlice []string

	if err := ValidateRepository(repoName, ""); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("repository part %q of object name invalid\n", repoName))
		errSlice = append(errSlice, err.Error())
	}

	if err := ValidateDirectoryName(path, false); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("package path part %q of object name invalid\n", path))
		errSlice = append(errSlice, "path "+path+invalidConst+err.Error()+"\n")
	}

	if err := ValidateK8SName(packageName); err != nil {
		errSlice = append(errSlice, fmt.Sprintf("package name part %q of object name invalid\n", packageName))
		errSlice = append(errSlice, "package name "+packageName+invalidConst+err.Error()+"\n")
	}

	return errSlice
}

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
