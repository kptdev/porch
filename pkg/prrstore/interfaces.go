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

// Package prrstore is the v1alpha2 system of record for PackageRevisionResources (PRR) content.
package prrstore

import (
	"context"

	"github.com/kptdev/porch/api/porch/key"
)

// Store-only lifecycles mark an unpushed Draft or Proposed revision that exists only in
// the PRRStore (push-drafts-to-git disabled). The marker is not a copy of the lifecycle
// (the CRD and git remain the record) and is cleared on publish.
const (
	StoreOnlyDraft    = "Draft"
	StoreOnlyProposed = "Proposed"
)

// StoreOnlyRevision identifies a revision that exists only in the PRRStore.
type StoreOnlyRevision struct {
	// Key carries the RepositoryKey passed to ListStoreOnly; Revision is 0.
	Key key.PackageRevisionKey
	// Lifecycle is StoreOnlyDraft or StoreOnlyProposed.
	Lifecycle string
}

// PRRReader serves PackageRevisionResources (read-only).
type PRRReader interface {
	// GetResources returns all resources for a package revision (empty map if not found).
	GetResources(ctx context.Context, prKey key.PackageRevisionKey) (map[string]string, error)

	// GetFilteredResources returns resources matching a selector (empty map if not found).
	GetFilteredResources(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string) (map[string]string, error)

	// GetResourceVersion returns the current resourceVersion for a PR, or "" if none.
	// Callers capture this as the base version for a compare-and-swap write-back.
	GetResourceVersion(ctx context.Context, prKey key.PackageRevisionKey) (string, error)

	// ListStoreOnly returns a repository's unpushed Draft/Proposed revisions, which git
	// discovery cannot see. Returned keys carry repoKey.
	ListStoreOnly(ctx context.Context, repoKey key.RepositoryKey) ([]StoreOnlyRevision, error)
}

// PRRWriter atomically writes resources and bumps resourceVersion.
type PRRWriter interface {
	// WriteResources atomically updates all resources for a PR and bumps version.
	// Returns the new resourceVersion (deterministic across retries).
	WriteResources(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string) (resourceVersion string, err error)

	// WriteResourcesAs writes like WriteResources and, in the same transaction, sets the
	// store-only marker to storeOnlyLifecycle, or clears it when empty. WriteResources and
	// WriteResourcesIfUnchanged leave the marker alone, so renders and PRR pushes to a
	// store-only draft keep it.
	WriteResourcesAs(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, storeOnlyLifecycle string) (resourceVersion string, err error)

	// WriteResourcesIfUnchanged writes only if the stored resourceVersion still equals
	// expectedVersion (compare-and-swap); otherwise it writes nothing and returns
	// conflict=true, so a stale render can't clobber newer content. Empty expectedVersion
	// means "write only if no entry exists".
	WriteResourcesIfUnchanged(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, expectedVersion string) (resourceVersion string, conflict bool, err error)

	// WriteResourcesAsIfUnchanged is WriteResourcesIfUnchanged that also sets the
	// store-only marker to storeOnlyLifecycle, or clears it when empty, when it writes.
	WriteResourcesAsIfUnchanged(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, storeOnlyLifecycle, expectedVersion string) (resourceVersion string, conflict bool, err error)

	// SetStoreOnlyLifecycle sets the store-only marker of an existing revision, or clears
	// it when empty, without changing its content or version. It does nothing if the
	// revision has no entry.
	SetStoreOnlyLifecycle(ctx context.Context, prKey key.PackageRevisionKey, storeOnlyLifecycle string) error
}

// PRRStore stores and retrieves PackageRevisionResources.
type PRRStore interface {
	PRRReader
	PRRWriter

	// DeleteResources removes all resources and version metadata for a PR (idempotent).
	DeleteResources(ctx context.Context, prKey key.PackageRevisionKey) error

	// DeleteRepositoryResources removes the resources, versions and markers of every
	// package revision in a repository (idempotent). The store has no foreign key to the
	// repository, so this is how its rows go when the repository is deleted.
	DeleteRepositoryResources(ctx context.Context, repoKey key.RepositoryKey) error

	// CheckSchema returns an error, saying how to fix it, if the store's tables are not
	// installed or are older than this code expects.
	CheckSchema(ctx context.Context) error
}
