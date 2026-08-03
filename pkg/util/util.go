// Copyright 2023-2026 The kpt Authors
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch"
	porchapiv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	pkgerrors "github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	registrationapi "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	invalidConst string = " invalid: "
	uuidSpace    string = "aac71d91-5c67-456f-8fd2-902ef6da820e"
)

func GetInClusterNamespace() (string, error) {
	ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", fmt.Errorf("failed to read in-cluster namespace: %w", err)
	}
	return string(ns), nil
}

func GetPorchApiServiceKey(ctx context.Context) (client.ObjectKey, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return client.ObjectKey{}, fmt.Errorf("failed to get K8s config: %w", err)
	}

	scheme := runtime.NewScheme()
	err = registrationapi.AddToScheme(scheme)
	if err != nil {
		return client.ObjectKey{}, fmt.Errorf("failed to add apiregistration API to scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return client.ObjectKey{}, fmt.Errorf("failed to create K8s client: %w", err)
	}

	apiSvc := registrationapi.APIService{}
	apiSvcName := porchapiv1alpha1.SchemeGroupVersion.Version + "." + porchapiv1alpha1.SchemeGroupVersion.Group
	err = c.Get(ctx, client.ObjectKey{
		Name: apiSvcName,
	}, &apiSvc)
	if err != nil {
		return client.ObjectKey{}, fmt.Errorf("failed to get APIService %q: %w", apiSvcName, err)
	}

	return client.ObjectKey{
		Namespace: apiSvc.Spec.Service.Namespace,
		Name:      apiSvc.Spec.Service.Name,
	}, nil
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

	if err := ValidateK8SName(string(workspace)); err != nil {
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

func GenerateUid(prefix string, kubeNs string, kubeName string) types.UID {
	space := uuid.MustParse(uuidSpace)
	buff := bytes.Buffer{}
	buff.WriteString(prefix)
	buff.WriteString(strings.ToLower(porchapiv1alpha1.SchemeGroupVersion.Identifier()))
	buff.WriteString("/")
	buff.WriteString(strings.ToLower(kubeNs))
	buff.WriteString("/")
	buff.WriteString(strings.ToLower(kubeName))
	return types.UID(uuid.NewSHA1(space, buff.Bytes()).String())
}

func SafeReverse[S ~[]E, E any](s S) {
	if s == nil {
		return
	}
	slices.Reverse(s)
}

func CompareObjectMeta(left metav1.ObjectMeta, right metav1.ObjectMeta) bool {
	if result := strings.Compare(left.Name, right.Name); result != 0 {
		return false
	}

	if result := strings.Compare(left.Namespace, right.Namespace); result != 0 {
		return false
	}

	if !mapsEqual(left.Labels, right.Labels) {
		return false
	}

	if !mapsEqual(left.Annotations, right.Annotations) {
		return false
	}

	if !slicesEqual(left.Finalizers, right.Finalizers) {
		return false
	}

	if !ownerRefsEqual(left.OwnerReferences, right.OwnerReferences) {
		return false
	}

	return true
}

func ownerRefEqual(a, b metav1.OwnerReference) bool {
	return a.APIVersion == b.APIVersion &&
		a.Kind == b.Kind &&
		a.Name == b.Name &&
		a.UID == b.UID &&
		boolPtrEqual(a.Controller, b.Controller) &&
		boolPtrEqual(a.BlockOwnerDeletion, b.BlockOwnerDeletion)
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func mapsEqual(a, b map[string]string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return maps.Equal(a, b)
}

func slicesEqual(a, b []string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return slices.Equal(a, b)
}

func ownerRefsEqual(a, b []metav1.OwnerReference) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return slices.EqualFunc(a, b, ownerRefEqual)
}

// RetryOnErrorConditional retries f up to retries times if it returns an error that matches shouldRetryFunc
func RetryOnErrorConditional(retries int, shouldRetryFunc func(error) bool, f func(retryNumber int) error) error {
	var err error
	for i := 1; i <= retries; i++ {
		err = f(i)
		if err == nil || !shouldRetryFunc(err) {
			return err
		}
	}
	return err
}

func GetRepoPackageRefFromUpstream(upstream *kptfilev1.Upstream) (upstreamRepoSpec *configapi.RepositorySpec, upstreamPackage, upstreamRef string, isManagedReference bool, err error) {

	isManagedReference = false

	if upstream == nil || upstream.Git == nil || upstream.Git.Repo == "" {
		err = pkgerrors.New("upstream does not contain a valid git repository")
		return
	}

	if validationErr := porchapi.IsValidSubpackageDir(upstream.Git.Directory); validationErr != nil {
		err = pkgerrors.Wrapf(validationErr, "git directory reference %q in upstream is invalid", upstream.Git.Directory)
		return
	}

	if upstream.Git.Ref == "" {
		err = pkgerrors.Errorf("git ref reference %q in upstream is invalid", upstream.Git.Ref)
		return
	}

	pattern := "(^|/)" + regexp.QuoteMeta(upstream.Git.Directory) + "/[^/]+$"
	isManagedReference, err = regexp.MatchString(pattern, upstream.Git.Ref)
	if err != nil {
		err = pkgerrors.Wrapf(err, "could not match upstream git ref %q against pattern %q", upstream.Git.Ref, pattern)
		return
	}

	if !isManagedReference {
		upstreamRepoSpec = &configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:      strings.TrimSuffix(upstream.Git.Repo, ".git"),
				Directory: strings.TrimSuffix(upstream.Git.Directory, "/"),
			},
		}

		upstreamPackage = strings.ReplaceAll(upstream.Git.Directory, "/", ".")
		upstreamRef = upstream.Git.Ref
		return
	}

	upstreamSplitRef := strings.Split(upstream.Git.Ref, "/")
	if len(upstreamSplitRef) < 2 || upstreamSplitRef[0] == "" || upstreamSplitRef[len(upstreamSplitRef)-1] == "" {
		err = pkgerrors.Errorf("git repository reference %q in upstream is invalid", upstream.Git.Ref)
		return
	}

	upstreamRef = upstreamSplitRef[len(upstreamSplitRef)-1]
	upstreamPackage = strings.ReplaceAll(upstream.Git.Directory, "/", ".")
	suffix := upstream.Git.Directory + "/" + upstreamRef
	if path.Clean(suffix) != suffix {
		err = pkgerrors.Errorf("git directory reference %q in upstream is invalid", upstream.Git.Directory)
		return
	}
	gitDir, found := strings.CutSuffix(upstream.Git.Ref, suffix)
	if !found {
		err = pkgerrors.Errorf("git directory %q and reference %q in upstream are inconsistent", upstream.Git.Directory, upstream.Git.Ref)
		return
	}

	upstreamRepoSpec = &configapi.RepositorySpec{
		Type: configapi.RepositoryTypeGit,
		Git: &configapi.GitRepository{
			Repo:      strings.TrimSuffix(upstream.Git.Repo, ".git"),
			Directory: strings.TrimSuffix(gitDir, "/"),
		},
	}

	return
}
