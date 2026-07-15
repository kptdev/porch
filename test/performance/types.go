// Copyright 2024 The Nephio Authors
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
	"time"
)

const (
	giteaRepoCreate       = "GITEA-REPO-CREATE"
	porchRepoCreate       = "PORCH-REPO-CREATE"
	repoWait              = "REPO-WAIT"
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
