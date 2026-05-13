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

// Package creation source specifications.
// In v1alpha2, the creation source is specified directly via PackageSource fields.
// Exactly one of Init, CloneFrom, CopyFrom, or Upgrade must be set when creating a PackageRevision.
// These fields are immutable after creation.

// PackageInitSpec defines the package initialization parameters.
// Used when creating a brand new package from scratch.
type PackageInitSpec struct {
	// Description is a short description of the package.
	Description string `json:"description,omitempty"`
	// Keywords is a list of keywords describing the package.
	Keywords []string `json:"keywords,omitempty"`
	// Site is a link to page with information about the package.
	Site string `json:"site,omitempty"`
}

// PackageUpgradeSpec defines the package upgrade parameters.
// Used when merging changes from a new upstream version into a local package.
type PackageUpgradeSpec struct {
	// OldUpstream is the reference to the original upstream package revision that is
	// the common ancestor of the local package and the new upstream package revision.
	OldUpstream PackageRevisionRef `json:"oldUpstream,omitempty"`

	// NewUpstream is the reference to the new upstream package revision that the
	// local package will be upgraded to.
	NewUpstream PackageRevisionRef `json:"newUpstream,omitempty"`

	// CurrentPackage is the reference to the current local package revision that
	// contains all the local changes on top of the OldUpstream package revision.
	CurrentPackage PackageRevisionRef `json:"currentPackage,omitempty"`

	// Strategy defines which strategy should be used to update the package. It defaults to 'resource-merge'.
	//  * resource-merge: Perform a structural comparison of the original /
	//    updated resources, and merge the changes into the local package.
	//  * fast-forward: Fail without updating if the local package was modified
	//    since it was fetched.
	//  * force-delete-replace: Wipe all the local changes to the package and replace
	//    it with the remote version.
	//  * copy-merge: Copy all the remote changes to the local package.
	Strategy PackageMergeStrategy `json:"strategy,omitempty"`
}

// PackageMergeStrategy defines the strategy for merging package changes
// +kubebuilder:validation:Enum=resource-merge;fast-forward;force-delete-replace;copy-merge
type PackageMergeStrategy string

const (
	ResourceMerge      PackageMergeStrategy = "resource-merge"
	FastForward        PackageMergeStrategy = "fast-forward"
	ForceDeleteReplace PackageMergeStrategy = "force-delete-replace"
	CopyMerge          PackageMergeStrategy = "copy-merge"
)
