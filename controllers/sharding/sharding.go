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

// Package sharding partitions controller work across pods by repository.
// Both controllers use the same shard key (ns/name) so repository work lands
// on one pod (single-writer invariant). Shard count is discovered from the
// StatefulSet's spec.replicas and kept in sync at runtime by MembershipProvider.
package sharding

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// Sharding holds the runtime shard identity and provides filter operations.
// ShardID is fixed (pod ordinal); numShards is atomic so the membership watcher
// can update it without reconciler locking. numShards <= 1 disables sharding.
type Sharding struct {
	ShardID   int
	numShards atomic.Int32
}

// NewSharding returns a Sharding with a fixed shard id and an initial shard count.
func NewSharding(shardID, numShards int) *Sharding {
	s := &Sharding{ShardID: shardID}
	if numShards < 0 || numShards > math.MaxInt32 {
		numShards = 1 // disable sharding if out of bounds
	}
	s.numShards.Store(int32(numShards))
	return s
}

// NumShards returns the current shard count.
func (s *Sharding) NumShards() int { return int(s.numShards.Load()) }

// SetNumShards sets the shard count. Used by MembershipProvider (and tests).
// Note: callers should trigger reconciliation of affected objects after this changes,
// as ownership may shift between shards. This is currently handled outside sharding
// (via MembershipProvider watching StatefulSet spec.replicas and triggering re-reconciliation).
// TODO(#1253-followup): implement proper work handoff/draining on ownership transitions.
func (s *Sharding) SetNumShards(n int) {
	if n < 0 || n > math.MaxInt32 {
		n = 1 // disable sharding if out of bounds
	}
	s.numShards.Store(int32(n))
}

// Disabled reports whether sharding is off (nil or count <= 1).
func (s *Sharding) Disabled() bool {
	if s == nil {
		return true
	}
	return s.NumShards() <= 1
}

// Owns reports whether this shard owns the repository. Deterministic so every
// controller computes the same owner. Returns true when sharding is disabled.
func (s *Sharding) Owns(repoNamespace, repoName string) bool {
	if s == nil {
		return true
	}
	n := s.NumShards()
	if n <= 1 {
		return true
	}
	return shardIndex(repoNamespace, repoName, n) == s.ShardID
}

func shardIndex(repoNamespace, repoName string, numShards int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(repoNamespace + "/" + repoName))
	return int(h.Sum32()) % numShards
}

// ResolveShardID returns the shard id from the pod's StatefulSet ordinal.
// Returns 0 if no numeric ordinal is found (gracefully handles Deployment pods).
func ResolveShardID(podName string) (int, error) {
	ordinal, err := ordinalFromPodName(podName)
	if err != nil {
		// Not a StatefulSet pod (no numeric ordinal suffix). Use shard 0.
		// Sharding will be disabled at runtime by the membership watcher anyway
		// when it fails to find the StatefulSet.
		return 0, nil
	}
	return ordinal, nil
}

// ordinalFromPodName extracts the StatefulSet ordinal from names like "pod-name-2".
// Returns 0, non-nil error if no numeric ordinal is found (e.g., Deployment pods).
func ordinalFromPodName(podName string) (int, error) {
	i := strings.LastIndex(podName, "-")
	if i < 0 || i == len(podName)-1 {
		return 0, fmt.Errorf("no ordinal suffix")
	}
	ordinal, err := strconv.Atoi(podName[i+1:])
	if err != nil {
		return 0, fmt.Errorf("ordinal suffix %q is not an integer", podName[i+1:])
	}
	return ordinal, nil
}

// PodNameFromEnv returns the pod name from downward-API env vars or hostname.
func PodNameFromEnv() string {
	for _, env := range []string{"POD_NAME", "HOSTNAME"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	name, _ := os.Hostname()
	return name
}
