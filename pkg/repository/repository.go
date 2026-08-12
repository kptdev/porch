// Copyright 2022, 2024-2026 The kpt Authors
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

// Package repository defines Porch generic repository interfaces and provides utility functions for repositories.
package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

// TODO: 	"sigs.k8s.io/kustomize/kyaml/filesys" FileSystem?
type PackageResources struct {
	Contents map[string]string
}

type PackageRevisionKey struct {
	PkgKey        PackageKey
	Revision      int
	WorkspaceName string
}

func (k PackageRevisionKey) String() string {
	return fmt.Sprintf("%s:%d:%s", k.PkgKey.String(), k.Revision, string(k.WorkspaceName))
}

func (k PackageRevisionKey) K8SNS() string {
	return k.RKey().Namespace
}

func (k PackageRevisionKey) K8SName() string {
	return ComposePkgRevObjName(k)
}

func K8SName2PkgRevWSName(k8sNamePkg, k8sName string) string {
	return k8sName[len(k8sNamePkg)+1:]
}

func PkgRevK8sName2Key(k8sNamespace, k8sName string) (PackageRevisionKey, error) {
	workspaceName := util.GetPRWorkspaceName(k8sName)

	conditionedK8SName := k8sName
	if strings.Contains(workspaceName, ".") {
		conditionedK8SName = k8sName[:len(k8sName)-len(workspaceName)] + strings.ReplaceAll(workspaceName, ".", "-")
	}

	parsedPRSlice := util.SplitIn3OnDelimiter(conditionedK8SName, ".")
	parsedPkgSlice := util.SplitIn3OnDelimiter(parsedPRSlice[0]+"."+parsedPRSlice[1], ".")

	packagePath := strings.ReplaceAll(parsedPkgSlice[1], ".", "/")
	if err := util.ValidPkgRevObjName(parsedPRSlice[0], packagePath, parsedPkgSlice[2], parsedPRSlice[2]); err != nil {
		return PackageRevisionKey{}, err
	}

	return PackageRevisionKey{
		PkgKey: PackageKey{
			RepoKey: RepositoryKey{
				Namespace: k8sNamespace,
				Name:      parsedPRSlice[0],
			},
			Path:    packagePath,
			Package: parsedPkgSlice[2],
		},
		WorkspaceName: workspaceName,
	}, nil
}

func (k PackageRevisionKey) DeepCopy(outKey *PackageRevisionKey) {
	k.PkgKey.DeepCopy(&outKey.PkgKey)
	outKey.Revision = k.Revision
	outKey.WorkspaceName = k.WorkspaceName
}

func (k PackageRevisionKey) PKey() PackageKey {
	return k.PkgKey
}

func (k PackageRevisionKey) RKey() RepositoryKey {
	return k.PkgKey.RepoKey
}

func (k PackageRevisionKey) Matches(other PackageRevisionKey) bool {
	if k.Revision != 0 && k.Revision != other.Revision {
		return false
	}

	if k.WorkspaceName != "" && k.WorkspaceName != other.WorkspaceName {
		return false
	}

	return k.PkgKey.Matches(other.PkgKey)
}

type PackageKey struct {
	RepoKey       RepositoryKey
	Path, Package string
}

func (k PackageKey) K8SNS() string {
	return k.RepoKey.Namespace
}

func (k PackageKey) K8SName() string {
	return ComposePkgObjName(k)
}

func PkgK8sName2Key(k8sNamespace, k8sName string) (PackageKey, error) {
	parsedPkgSlice := util.SplitIn3OnDelimiter(k8sName, ".")

	packagePath := strings.ReplaceAll(parsedPkgSlice[1], ".", "/")
	if err := util.ValidPkgObjName(parsedPkgSlice[0], packagePath, parsedPkgSlice[2]); err != nil {
		return PackageKey{}, err
	}

	return PackageKey{
		RepoKey: RepositoryKey{
			Namespace: k8sNamespace,
			Name:      parsedPkgSlice[0],
		},
		Path:    packagePath,
		Package: parsedPkgSlice[2],
	}, nil
}

func (k PackageKey) String() string {
	return fmt.Sprintf("%s:%s:%s", k.RepoKey.String(), k.Path, k.Package)
}

func (k PackageKey) DeepCopy(outKey *PackageKey) {
	k.RepoKey.DeepCopy(&outKey.RepoKey)
	outKey.Path = k.Path
	outKey.Package = k.Package
}

func (k PackageKey) ToPkgPathname() string {
	return filepath.Join(k.Path, k.Package)
}

func (k PackageKey) ToFullPathname() string {
	return filepath.Join(k.RepoKey.Path, k.Path, k.Package)
}

func K8SName2PkgName(k8sName string) string {
	lastDotPos := strings.LastIndex(k8sName, ".")

	return k8sName[lastDotPos+1:]
}

func FromFullPathname(repoKey RepositoryKey, fullpath string) PackageKey {
	path, name := SplitPackagePathName(fullpath)

	return PackageKey{
		RepoKey: repoKey,
		Path:    path,
		Package: name,
	}
}

func SplitPackagePathName(fullpath string) (path, name string) {
	pkgPath := strings.Trim(fullpath, "/")
	slashIndex := strings.LastIndex(pkgPath, "/")

	if slashIndex >= 0 {
		return pkgPath[:slashIndex], pkgPath[slashIndex+1:]
	}

	return "", pkgPath
}

func (k PackageKey) RKey() RepositoryKey {
	return k.RepoKey
}

func (k PackageKey) Matches(other PackageKey) bool {
	if k.Path != "" && k.Path != other.Path {
		return false
	}

	if k.Package != "" && k.Package != other.Package {
		return false
	}

	return k.RepoKey.Matches(other.RepoKey)
}

type RepositoryKey struct {
	Namespace, Name, Path, PlaceholderWSname string
}

func (k RepositoryKey) K8SNS() string {
	return k.Namespace
}

func (k RepositoryKey) K8SName() string {
	return k.Name
}

func (k RepositoryKey) String() string {
	return fmt.Sprintf("%s:%s:%s:%s", k.Namespace, k.Name, k.Path, string(k.PlaceholderWSname))
}

func (k RepositoryKey) DeepCopy(outKey *RepositoryKey) {
	outKey.Name = k.Name
	outKey.Namespace = k.Namespace
	outKey.Path = k.Path
	outKey.PlaceholderWSname = k.PlaceholderWSname
}

func (k RepositoryKey) Matches(other RepositoryKey) bool {
	if k.Namespace != "" && k.Namespace != other.Namespace {
		return false
	}
	if k.Name != "" && k.Name != other.Name {
		return false
	}

	if k.Path != "" && k.Path != other.Path {
		return false
	}

	if k.PlaceholderWSname != "" && k.PlaceholderWSname != other.PlaceholderWSname {
		return false
	}

	return true
}

// PackageRevision is an abstract package version.
// We have a single object for both Revision and Resources, because conceptually they are one object.
// The best way we've found (so far) to represent them in k8s is as two resources, but they map to the same object.
// Interesting reading: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#differing-representations
type PackageRevision interface {
	KubeObjectNamespace() string
	KubeObjectName() string
	Key() PackageRevisionKey

	// UID returns a unique identifier for the PackageRevision.
	UID() types.UID

	// Lifecycle returns the current lifecycle state of the package.
	Lifecycle(ctx context.Context) porchapi.PackageRevisionLifecycle

	// UpdateLifecycle updates the desired lifecycle of the package. This can only
	// be used for Published package revisions to go from Published to DeletionProposed
	// or vice versa. Draft revisions should use PackageDraft.UpdateLifecycle.
	UpdateLifecycle(ctx context.Context, lifecycle porchapi.PackageRevisionLifecycle) error

	// GetPackageRevision returns the PackageRevision ("DRY") API representation of this package-revision
	GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error)

	// GetResources returns the PackageRevisionResources ("WET") API representation of this package-revision
	// TODO: return PackageResources or filesystem abstraction?
	GetResources(ctx context.Context) (*porchapi.PackageRevisionResources, error)

	// GetUpstreamLock returns the kpt lock information.
	GetUpstreamLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error)

	// GetKptfile returns the Kptfile for the package
	GetKptfile(ctx context.Context) (kptfilev1.KptFile, error)

	// GetLock returns the current revision's lock information.
	// This will be the upstream info for downstream revisions.
	GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error)

	// ResourceVersion returns the Kube resource version of the package
	ResourceVersion() string

	// Create the main package revision
	// TODO: This is a git thing and probably shouldn't be on the generic PackageRevision interface
	ToMainPackageRevision(ctx context.Context) PackageRevision

	// Get the Kubernetes metadata for the package revision
	GetMeta() metav1.ObjectMeta

	// Set the Kubernetes metadata for the package revision
	SetMeta(ctx context.Context, meta metav1.ObjectMeta) error

	// GetCommitInfo returns the commit time and author for the package revision.
	// Returns zero values for backends that don't track commits (e.g. OCI).
	GetCommitInfo() (time.Time, string)

	// IsLatestRevision returns true if this is the latest published revision of its package.
	IsLatestRevision() bool
}

// Package is an abstract package.
type Package interface {
	KubeObjectNamespace() string
	KubeObjectName() string
	Key() PackageKey

	// GetPackage returns the object representing this package
	GetPackage(ctx context.Context) *porchapi.PorchPackage

	// GetLatestRevision returns the name of the package revision that is the "latest" package
	// revision belonging to this package
	GetLatestRevision(ctx context.Context) int
}

type PackageRevisionDraft interface {
	Key() PackageRevisionKey
	GetMeta() metav1.ObjectMeta
	UpdateResources(context.Context, *porchapi.PackageRevisionResources, *porchapi.Task) error
	// Updates desired lifecycle of the package. The lifecycle is applied on Close.
	UpdateLifecycle(context.Context, porchapi.PackageRevisionLifecycle) error
}

// ListPackageRevisionFilter is a predicate for filtering PackageRevision objects;
// only matching PackageRevision objects will be returned.
type ListPackageRevisionFilter struct {
	Key PackageRevisionKey

	// Lifecycle matches the spec.lifecycle of the package
	Lifecycles []porchapi.PackageRevisionLifecycle

	// KptfileLabels matches labels specified in the Kptfile
	KptfileLabels map[string]string

	Label labels.Selector
}

// Matches returns true if the provided PackageRevision satisfies the conditions in the filter.
func (f *ListPackageRevisionFilter) Matches(ctx context.Context, p PackageRevision) bool {
	if !f.Key.Matches(p.Key()) {
		return false
	}

	if len(f.Lifecycles) > 0 && !slices.Contains(f.Lifecycles, p.Lifecycle(ctx)) {
		return false
	}

	if len(f.KptfileLabels) > 0 {
		packageRevision, err := p.GetPackageRevision(ctx)
		if err != nil {
			return false
		}

		if packageRevision.Spec.PackageMetadata == nil {
			return false
		}

		for labelKey, expectedlValue := range f.KptfileLabels {
			actualValue, exists := packageRevision.Spec.PackageMetadata.Labels[labelKey]
			if !exists || actualValue != expectedlValue {
				return false
			}
		}
	}

	if !f.MatchesLabels(ctx, p) {
		return false
	}

	return true
}

func (f *ListPackageRevisionFilter) MatchesNamespace(namespace string) (bool, string) {
	filteredNamespace := f.Key.RKey().Namespace
	return (filteredNamespace == "" || namespace == filteredNamespace), filteredNamespace
}

func (f *ListPackageRevisionFilter) FilteredRepository() string {
	return f.Key.PKey().RKey().Name
}

// MatchesLabels returns true if the filter either:
//   - does not filter on labels (nil Label field), OR
//   - matches on labels of the provided PackageRevision
func (f *ListPackageRevisionFilter) MatchesLabels(ctx context.Context, p PackageRevision) bool {
	if f.Label != nil {
		return f.Label.Matches(getPkgRevLabels(p))
	}

	return true
}

// getPkgRevLabels returns the metadata labels of a given PackageRevision for filtering purposes.
// The labels are returned in the form of a Kubernetes labels.Set which can be easily matched
// against a labels.Selector which came in in a list request.
func getPkgRevLabels(p PackageRevision) labels.Set {
	labelSet := func() labels.Set {
		labels := p.GetMeta().Labels
		if labels == nil {
			labels = make(map[string]string, 1)
		}
		return labels
	}()
	isLatest := p.IsLatestRevision()
	if isLatest {
		labelSet[porchapi.LatestPackageRevisionKey] = porchapi.LatestPackageRevisionValue
	}

	return labelSet
}

// ListPackageFilter is a predicate for filtering Package objects;
// only matching Package objects will be returned.
type ListPackageFilter struct {
	Key PackageKey
}

// Matches returns true if the provided Package satisfies the conditions in the filter.
func (f *ListPackageFilter) Matches(p Package) bool {
	return f.Key.Matches(p.Key())
}

// Repository is the interface for interacting with packages in repositories
// TODO: we may need interface to manage repositories too. Stay tuned.
type Repository interface {
	KubeObjectNamespace() string
	KubeObjectName() string
	Key() RepositoryKey

	// ListPackageRevisions lists the existing package revisions in the repository
	ListPackageRevisions(ctx context.Context, filter ListPackageRevisionFilter) ([]PackageRevision, error)

	// CreatePackageRevision creates a new package revision
	CreatePackageRevisionDraft(ctx context.Context, obj *porchapi.PackageRevision) (PackageRevisionDraft, error)

	// ClosePackageRevisionDraft closes out a Package Revision Draft
	ClosePackageRevisionDraft(ctx context.Context, prd PackageRevisionDraft, version int) (PackageRevision, error)

	// DeletePackageRevision deletes a package revision
	DeletePackageRevision(ctx context.Context, old PackageRevision) error

	// UpdatePackageRevision updates a package
	UpdatePackageRevision(ctx context.Context, old PackageRevision) (PackageRevisionDraft, error)

	// ListPackages lists all packages in the repository
	ListPackages(ctx context.Context, filter ListPackageFilter) ([]Package, error)

	// Version returns a string that is guaranteed to be different if any change has been made to the repo contents
	Version(ctx context.Context) (string, error)

	// BranchCommitHash returns the commit hash of the configured branch for git repositories.
	// Returns empty string for OCI repositories or if branch doesn't exist.
	BranchCommitHash(ctx context.Context) (string, error)

	// Close cleans up any resources associated with the repository
	Close(ctx context.Context) error

	// Refresh the repository
	Refresh(ctx context.Context) error
}

// The definitions below would be more appropriately located in a package usable by any Porch component.
// They are located in repository package because repository is one such package though thematically
// they rather belong to a package of their own.

type Credential interface {
	Valid() bool
	ToAuthMethod() transport.AuthMethod
	ToString() string
}

type CredentialResolver interface {
	ResolveCredential(ctx context.Context, namespace, name string) (Credential, error)
}

type UserInfo struct {
	Name  string
	Email string
}

// UserInfoProvider providers name of the authenticated user on whose behalf the request
// is being processed.
type UserInfoProvider interface {
	// GetUserInfo returns the information about the user on whose behalf the request is being
	// processed, if any. If user cannot be determnined, returns nil.
	GetUserInfo(ctx context.Context) *UserInfo
}

// IsNotFoundError checks if an error indicates that a package or resource was not found.
// This is used to handle cases where a package exists in cache but not in external repository.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	errorStr := strings.ToLower(err.Error())
	return strings.Contains(errorStr, "not found") ||
		strings.Contains(errorStr, "does not exist") ||
		strings.Contains(errorStr, "no such") ||
		strings.Contains(errorStr, "404")
}
