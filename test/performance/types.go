// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"fmt"
	"time"
)

// PorchAPIVersion identifies which Porch PackageRevision API the performance test uses.
type PorchAPIVersion string

const (
	PorchAPIV1Alpha1 PorchAPIVersion = "v1alpha1"
	PorchAPIV1Alpha2 PorchAPIVersion = "v1alpha2"
)

// Repository-level operation metric keys.
const (
	giteaRepoCreate = "GITEA-REPO-CREATE"
	porchRepoCreate = "PORCH-REPO-CREATE"
	repoWait        = "REPO-WAIT"
)

// Package revision operation metric keys shared across API versions.
const (
	pkgRevList            = "LIST"
	pkgRevGet             = "GET"
	pkgRevGetProposed     = "GET-PROPOSED"
	pkgRevResourcesGet    = "GET-RESOURCES"
	pkgRevCreate          = "CREATE"
	pkgRevUpdate          = "UPDATE"
	pkgRevPropose         = "PROPOSE"
	pkgRevPublished       = "APPROVE"
	pkgRevProposeDeletion = "PROPOSE-DELETION"
	pkgRevDelete          = "DELETE"
)

// v1alpha2 controller reconciliation operation metric keys.
const (
	pkgRevWaitReady     = "WAIT-READY"
	pkgRevWaitRendered  = "WAIT-RENDERED"
	pkgRevWaitPublished = "WAIT-PUBLISHED"
)

func ParsePorchAPIVersion(version string) (PorchAPIVersion, error) {
	switch PorchAPIVersion(version) {
	case PorchAPIV1Alpha1, PorchAPIV1Alpha2:
		return PorchAPIVersion(version), nil
	default:
		return "", fmt.Errorf("unsupported porch API version %q (supported: %s, %s)", version, PorchAPIV1Alpha1, PorchAPIV1Alpha2)
	}
}

type OperationMetrics struct {
	Operation string
	Duration  time.Duration
	Error     error
	Timestamp time.Time // When the operation started
}

type TestMetrics struct {
	RepoName      string
	repoOps       map[string]OperationMetrics
	pkgRevMetrics map[string]map[int]PackageRevisionMetrics
}

type PackageRevisionMetrics struct {
	pkgName  string
	Revision int
	Metrics  map[string]OperationMetrics
}

type Stats struct {
	Min   time.Duration
	Max   time.Duration
	Total time.Duration
	Count int
}

// DeletionCandidate identifies a package revision targeted for deletion cleanup.
type DeletionCandidate struct {
	Name          string
	RepoName      string
	PackageName   string
	WorkspaceName string
	RevisionNum   int
}
