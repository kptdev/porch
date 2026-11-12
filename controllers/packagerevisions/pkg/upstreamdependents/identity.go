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

// Package upstreamdependents holds identity, naming and verification rules for
// UpstreamDependents objects.
package upstreamdependents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

const (
	// AnnotationUpstreamKey holds an object's full identity.
	AnnotationUpstreamKey = "porch.kpt.dev/upstream-key"

	// MaxDependents caps entries per object. Beyond it the object is truncated.
	MaxDependents = 4000

	// nameHashLen is the hex length of the identity hash in an object name.
	nameHashLen = 12

	// prefixMaxLen caps the readable part of an object name.
	prefixMaxLen = 40

	fallbackPrefix = "upstream"
)

// IdentityForPackageRevision identifies an in-cluster upstream by name, which survives rehoming.
func IdentityForPackageRevision(name string) string {
	return name
}

// IdentityForLocator identifies an external upstream as `repo|dir|ref`, commit excluded.
// It returns "" when the locator has no git repository.
func IdentityForLocator(loc *porchv1alpha2.Locator) string {
	if loc == nil || loc.Git == nil || loc.Git.Repo == "" {
		return ""
	}
	return loc.Git.Repo + "|" + loc.Git.Directory + "|" + loc.Git.Ref
}

// ObjectName returns `<prefix>-<hash>` for an identity. The hash is the first 12 hex characters
// of its SHA-256, so the name is deterministic.
func ObjectName(identity, readable string) string {
	sum := sha256.Sum256([]byte(identity))
	hash := hex.EncodeToString(sum[:])[:nameHashLen]

	prefix := sanitize(readable)
	if prefix == "" {
		prefix = fallbackPrefix
	}
	return prefix + "-" + hash
}

// ReadableForLocator returns the directory and ref as the readable prefix hint.
func ReadableForLocator(loc *porchv1alpha2.Locator) string {
	if loc == nil || loc.Git == nil {
		return ""
	}
	return loc.Git.Directory + " " + loc.Git.Ref
}

// sanitize lowercases s, maps each run of characters outside a-z0-9 to '-', trims '-' from both
// ends, and truncates to prefixMaxLen.
func sanitize(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > prefixMaxLen {
		out = strings.TrimRight(out[:prefixMaxLen], "-")
	}
	return out
}

// CheckIdentity refuses a write if the object holds a different identity under the same name.
// Keys must never merge.
func CheckIdentity(annotations map[string]string, identity string) error {
	existing, ok := annotations[AnnotationUpstreamKey]
	if !ok || existing == identity {
		return nil
	}
	return fmt.Errorf("object already holds upstream identity %q, refusing to write %q: manual review required",
		existing, identity)
}
