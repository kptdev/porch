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

// PRRReader serves PackageRevisionResources (read-only).
type PRRReader interface {
	// GetResources returns all resources for a package revision (empty map if not found).
	GetResources(ctx context.Context, prKey key.PackageRevisionKey) (map[string]string, error)

	// GetFilteredResources returns resources matching a selector (empty map if not found).
	GetFilteredResources(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string) (map[string]string, error)

	// GetResourceVersion returns the current resourceVersion for a PR, or "" if none.
	// Callers capture this as the base version for a compare-and-swap write-back.
	GetResourceVersion(ctx context.Context, prKey key.PackageRevisionKey) (string, error)
}

// PRRWriter atomically writes resources and bumps resourceVersion.
type PRRWriter interface {
	// WriteResources atomically updates all resources for a PR and bumps version.
	// Returns the new resourceVersion (deterministic across retries).
	WriteResources(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string) (resourceVersion string, err error)

	// WriteResourcesIfUnchanged writes only if the stored resourceVersion still equals
	// expectedVersion (compare-and-swap); otherwise it writes nothing and returns
	// conflict=true, so a stale render can't clobber newer content. Empty expectedVersion
	// means "write only if no entry exists".
	WriteResourcesIfUnchanged(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, expectedVersion string) (resourceVersion string, conflict bool, err error)
}

// PRRStore stores and retrieves PackageRevisionResources.
type PRRStore interface {
	PRRReader
	PRRWriter

	// DeleteResources removes all resources and version metadata for a PR (idempotent).
	DeleteResources(ctx context.Context, prKey key.PackageRevisionKey) error
}
