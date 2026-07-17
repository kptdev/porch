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

var repoOperations = []string{
	giteaRepoCreate,
	porchRepoCreate,
	repoWait,
}

var v1alpha1PkgRevOperations = []string{
	pkgRevList,
	pkgRevCreate,
	pkgRevResourcesGet,
	pkgRevUpdate,
	pkgRevGet,
	pkgRevPropose,
	pkgRevGetProposed,
	pkgRevPublished,
	pkgRevProposeDeletion,
	pkgRevDelete,
}

var v1alpha2PkgRevOperations = []string{
	pkgRevList,
	pkgRevCreate,
	pkgRevWaitReady,
	pkgRevResourcesGet,
	pkgRevUpdate,
	pkgRevWaitRendered,
	pkgRevGet,
	pkgRevPropose,
	pkgRevGetProposed,
	pkgRevPublished,
	pkgRevWaitPublished,
	pkgRevProposeDeletion,
	pkgRevDelete,
}

var operationHeadings = map[string]string{
	giteaRepoCreate:       "Create Gitea Repository ",
	porchRepoCreate:       "Create Porch Repository ",
	repoWait:              "Repository Ready Wait",
	pkgRevList:            "Package Revision List",
	pkgRevCreate:          "Package Revision Create",
	pkgRevWaitReady:       "Package Revision Wait Ready",
	pkgRevResourcesGet:    "Package Revision Get Resources",
	pkgRevUpdate:          "Package Revision Update",
	pkgRevWaitRendered:    "Package Revision Wait Rendered",
	pkgRevGet:             "Package Revision Get",
	pkgRevPropose:         "Package Revision Propose",
	pkgRevGetProposed:     "Package Revision Get (Proposed)",
	pkgRevPublished:       "Package Revision Approve/Publish",
	pkgRevWaitPublished:   "Package Revision Wait Published",
	pkgRevProposeDeletion: "Package Revision Propose Deletion",
	pkgRevDelete:          "Package Revision Delete",
}

func (v PorchAPIVersion) RepoOperations() []string {
	return append([]string(nil), repoOperations...)
}

func (v PorchAPIVersion) PkgRevOperations() []string {
	switch v {
	case PorchAPIV1Alpha2:
		return append([]string(nil), v1alpha2PkgRevOperations...)
	default:
		return append([]string(nil), v1alpha1PkgRevOperations...)
	}
}

func (v PorchAPIVersion) AllOperations() []string {
	return append(v.RepoOperations(), v.PkgRevOperations()...)
}

func (v PorchAPIVersion) LifecyclePkgRevOperations() []string {
	ops := v.PkgRevOperations()
	lifecycle := make([]string, 0, len(ops))
	for _, op := range ops {
		if op != pkgRevProposeDeletion && op != pkgRevDelete {
			lifecycle = append(lifecycle, op)
		}
	}
	return lifecycle
}

func operationHeading(opKey string) string {
	if heading := operationHeadings[opKey]; heading != "" {
		return heading
	}
	return opKey
}

func isDeletionOperation(opKey string) bool {
	return opKey == pkgRevProposeDeletion || opKey == pkgRevDelete
}
