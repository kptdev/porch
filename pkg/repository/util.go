// Copyright 2022, 2025 The kpt and Nephio Authors
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
	"path"
	"strconv"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/nephio-project/porch/api/porch/v1alpha1"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	"github.com/nephio-project/porch/pkg/util"
	pkgerrors "github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

func ToAPIReadinessGates(kf kptfilev1.KptFile) []porchapi.ReadinessGate {
	var readinessGates []porchapi.ReadinessGate
	if kf.Info != nil {
		for _, rg := range kf.Info.ReadinessGates {
			readinessGates = append(readinessGates, porchapi.ReadinessGate{
				ConditionType: rg.ConditionType,
			})
		}
	}
	return readinessGates
}

func ToAPIConditions(kf kptfilev1.KptFile) []porchapi.Condition {
	var conditions []porchapi.Condition
	if kf.Status != nil && kf.Status.Conditions != nil {
		for _, s := range kf.Status.Conditions {
			conditions = append(conditions, porchapi.Condition{
				Type:    s.Type,
				Status:  toAPIConditionStatus(s.Status),
				Reason:  s.Reason,
				Message: s.Message,
			})
		}
	}
	return conditions
}

func toAPIConditionStatus(s kptfilev1.ConditionStatus) porchapi.ConditionStatus {
	switch s {
	case kptfilev1.ConditionTrue:
		return porchapi.ConditionTrue
	case kptfilev1.ConditionFalse:
		return porchapi.ConditionFalse
	case kptfilev1.ConditionUnknown:
		return porchapi.ConditionUnknown
	default:
		panic(fmt.Errorf("unknown condition status: %v", s))
	}
}

func UpsertAPICondition(conditions []porchapi.Condition, condition porchapi.Condition) []porchapi.Condition {
	updatedConditions := []porchapi.Condition{}

	for _, conditionItem := range conditions {
		if conditionItem.Type != condition.Type {
			updatedConditions = append(updatedConditions, conditionItem)
		}
	}

	updatedConditions = append(updatedConditions, condition)

	return updatedConditions
}

// AnyBlockOwnerDeletionSet checks whether there are any ownerReferences in the Object
// which have blockOwnerDeletion enabled (meaning either nil or true).
func AnyBlockOwnerDeletionSet(obj metav1.ObjectMeta) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.BlockOwnerDeletion == nil || *owner.BlockOwnerDeletion {
			return true
		}
	}
	return false
}

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

func ComposePkgObjName(key PackageKey) string {
	return util.ComposePkgObjName(key.RKey().Name, key.Path, key.Package)
}

func ComposePkgRevObjName(key PackageRevisionKey) string {
	return util.ComposePkgRevObjName(key.RKey().Name, key.PKey().Path, key.PKey().Package, key.WorkspaceName)
}

func PrSlice2Map(prSlice []PackageRevision) map[PackageRevisionKey]PackageRevision {
	prMap := make(map[PackageRevisionKey]PackageRevision)

	for _, pr := range prSlice {
		prMap[pr.Key()] = pr
	}

	return prMap
}

func KptUpstreamLock2APIUpstreamLock(kptLock kptfilev1.Locator) *porchapi.Locator {
	porchLock := &porchapi.Locator{}

	porchLock.Type = porchapi.OriginType(kptLock.Type)
	if kptLock.Git != nil {
		porchLock.Git = &porchapi.GitLock{
			Repo:      kptLock.Git.Repo,
			Directory: kptLock.Git.Directory,
			Commit:    kptLock.Git.Commit,
			Ref:       kptLock.Git.Ref,
		}
	}

	return porchLock
}

func KptUpstreamLock2KptUpstream(kptLock kptfilev1.Locator) kptfilev1.Upstream {
	kptUpstream := kptfilev1.Upstream{}

	kptUpstream.Type = kptLock.Type
	if kptLock.Git != nil {
		kptUpstream.Git = &kptfilev1.Git{
			Repo:      kptLock.Git.Repo,
			Directory: kptLock.Git.Directory,
			Ref:       kptLock.Git.Ref,
		}
	}

	return kptUpstream
}

// ValidatePackagePathOverlap checks for path conflicts with existing packages
func ValidatePackagePathOverlap(newPr *porchapi.PackageRevision, existingRevs []PackageRevision) error {
	existingPaths := make(map[string]bool)
	for _, r := range existingRevs {
		pkgPath := r.Key().PkgKey.Package
		if pkgPath == newPr.Spec.PackageName && r.Key().PkgKey.RepoKey.Name == newPr.Spec.RepositoryName {
			return fmt.Errorf("package %q already exists in repository %q", newPr.Spec.PackageName, newPr.Spec.RepositoryName)
		}
		if r.Key().PkgKey.RepoKey.Name == newPr.Spec.RepositoryName {
			existingPaths[pkgPath] = true
		}
	}

	newPath := newPr.Spec.PackageName
	for existingPath := range existingPaths {
		if PathsOverlap(newPath, existingPath) {
			return fmt.Errorf("package path %q conflicts with existing package %q: packages cannot be nested", newPath, existingPath)
		}
	}
	return nil
}

// PathsOverlap checks if two package paths would create a nesting conflict
func PathsOverlap(path1, path2 string) bool {
	if path1 == path2 {
		return false
	}
	if strings.HasPrefix(path2+"/", path1+"/") {
		return true
	}
	if strings.HasPrefix(path1+"/", path2+"/") {
		return true
	}
	return false
}

func PackageRevisionIsPlaceholder(ctx context.Context, namespace string, referenceResolver ReferenceResolver, packageRevision PackageRevision) (bool, error) {
	if packageRevision.Key().Revision != -1 {
		return false, nil
	}

	var upstreamRepo configapi.Repository
	err := referenceResolver.ResolveReference(ctx, namespace, packageRevision.Key().RKey().Name, &upstreamRepo)
	if err != nil {
		return false, pkgerrors.Wrapf(err, "failed to resolve repository reference for %q when checking placeholder revision: %v", packageRevision.Key().RKey().Name, err)
	}

	if upstreamRepo.Spec.Git != nil && packageRevision.Key().WorkspaceName == upstreamRepo.Spec.Git.Branch {
		return true, nil
	}

	return false, nil
}

func WriteResourcesToFS(fs filesys.FileSystem, rootDir string, resources map[string]string) (string, error) {
	if rootDir != "" {
		if err := fs.MkdirAll(rootDir); err != nil {
			return "", err
		}
	}

	var packageDir string
	for k, v := range resources {
		dir := path.Dir(k)
		if dir == "." {
			dir = "/"
		}

		fullDir := path.Join(rootDir, dir)
		if err := fs.MkdirAll(fullDir); err != nil {
			return "", err
		}
		if err := fs.WriteFile(path.Join(fullDir, path.Base(k)), []byte(v)); err != nil {
			return "", err
		}
		if path.Base(k) == "Kptfile" {
			if packageDir == "" || dir == "/" || strings.HasPrefix(packageDir, dir+"/") {
				packageDir = dir
			}
		}
	}
	return packageDir, nil
}
