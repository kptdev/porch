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

package upstreamdependents

import (
	"context"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

// Lookup reports whether a recorded dependent exists and still references the upstream at its
// subpackage path. A non-nil error means the lookup failed.
type Lookup func(ctx context.Context, entry porchv1alpha2.UpstreamDependent) (exists, references bool, err error)

// Verdict is the outcome of Verify.
type Verdict struct {
	// Blocked: the upstream must not be deleted.
	Blocked bool
	// Blocker: the first live dependent, if any.
	Blocker *porchv1alpha2.UpstreamDependent
	// Reason: why the upstream is blocked without a live dependent.
	Reason string
}

// Verify decides whether an UpstreamDependents status blocks deletion of its upstream.
//
// A truncated object blocks. Each entry is checked: a missing or non-referencing dependent is
// stale and skipped, and the first live match blocks. A lookup error blocks and is returned.
// Entries are hints; the live dependent decides.
func Verify(ctx context.Context, status porchv1alpha2.UpstreamDependentsStatus, lookup Lookup) (Verdict, error) {
	if status.Truncated {
		return Verdict{Blocked: true, Reason: "dependent list is truncated"}, nil
	}

	for _, entry := range status.Dependents {
		exists, references, err := lookup(ctx, entry)
		if err != nil {
			return Verdict{Blocked: true, Reason: "dependent could not be verified"}, err
		}
		if !exists || !references {
			continue
		}
		blocker := entry
		return Verdict{Blocked: true, Blocker: &blocker}, nil
	}

	return Verdict{}, nil
}
