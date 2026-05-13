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

// RepositoryType specifies the type of repository
// +kubebuilder:validation:Enum=git
type RepositoryType string

const (
	RepositoryTypeGit RepositoryType = "git"
)

// UpstreamPackage specifies an upstream package source.
// Exactly one of UpstreamRef specification or Git specification must be set.
type UpstreamPackage struct {
	// Type of the repository (i.e. git). If empty, upstreamRef will be used.
	Type RepositoryType `json:"type,omitempty"`

	// Git upstream package specification. Required if type is git.
	Git *GitPackage `json:"git,omitempty"`

	// UpstreamRef is the reference to the package from a registered repository.
	UpstreamRef *PackageRevisionRef `json:"upstreamRef,omitempty"`
}

// GitPackage describes a package stored in a Git repository
type GitPackage struct {
	// Repo is the address of the Git repository, for example:
	// https://github.com/GoogleCloudPlatform/blueprints.git
	Repo string `json:"repo"`

	// Ref is the git ref containing the package. Ref can be a branch, tag, or commit SHA.
	Ref string `json:"ref"`

	// Directory within the Git repository where the packages are stored.
	Directory string `json:"directory"`

	// SecretRef is a reference to secret containing authentication credentials.
	SecretRef SecretRef `json:"secretRef,omitempty"`
}

// NameRef is a reference to a named resource in the same namespace.
type NameRef struct {
	Name string `json:"name"`
}

// Typed references for clarity at point of use.
type SecretRef = NameRef
type PackageRevisionRef = NameRef
type RepositoryRef = NameRef
