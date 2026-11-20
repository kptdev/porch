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

package sharding

import (
	"fmt"
	"testing"
)

func TestDisabled(t *testing.T) {
	for _, n := range []int{0, 1} {
		s := NewSharding(0, n)
		if !s.Disabled() {
			t.Errorf("NumShards=%d: Disabled() = false, want true", n)
		}
		// When disabled, every repo is owned regardless of shard id.
		if !s.Owns("any-ns", "any-repo") {
			t.Errorf("NumShards=%d: Owns() = false, want true (sharding disabled)", n)
		}
	}
	if NewSharding(0, 2).Disabled() {
		t.Error("NumShards=2: Disabled() = true, want false")
	}
}

func TestOwns_Deterministic(t *testing.T) {
	s := NewSharding(0, 4)
	// Same input must always map to the same answer.
	first := s.Owns("ns", "repo-a")
	for i := 0; i < 100; i++ {
		if s.Owns("ns", "repo-a") != first {
			t.Fatalf("Owns() not deterministic for ns/repo-a")
		}
	}
}

func TestOwns_PartitionIsComplete_AndDisjoint(t *testing.T) {
	// Every repo must be owned by exactly one shard across the full shard set.
	const numShards = 5
	shards := make([]*Sharding, numShards)
	for i := range shards {
		shards[i] = NewSharding(i, numShards)
	}

	for r := 0; r < 1000; r++ {
		repo := fmt.Sprintf("repo-%d", r)
		owners := 0
		for _, s := range shards {
			if s.Owns("default", repo) {
				owners++
			}
		}
		if owners != 1 {
			t.Errorf("repo %q owned by %d shards, want exactly 1", repo, owners)
		}
	}
}

func TestOwns_DistributionSkew(t *testing.T) {
	// With many repos the hash should spread them roughly evenly. We assert a
	// loose bound (no shard gets more than 2x the fair share) to catch a badly
	// broken hash without being flaky.
	const (
		numShards = 4
		numRepos  = 10000
	)
	counts := make([]int, numShards)
	shards := make([]*Sharding, numShards)
	for i := range shards {
		shards[i] = NewSharding(i, numShards)
	}
	for r := 0; r < numRepos; r++ {
		repo := fmt.Sprintf("ns-%d/repo-%d", r%7, r)
		for i := 0; i < numShards; i++ {
			if shards[i].Owns("", repo) {
				counts[i]++
				break
			}
		}
	}
	fair := numRepos / numShards
	for i, got := range counts {
		if got == 0 {
			t.Errorf("shard %d got 0 repos; hash not distributing", i)
		}
		if got > 2*fair {
			t.Errorf("shard %d got %d repos (>2x fair share %d); distribution too skewed", i, got, fair)
		}
	}
	t.Logf("distribution across %d shards for %d repos: %v (fair=%d)", numShards, numRepos, counts, fair)
}

func TestSetNumShards_DynamicMembership(t *testing.T) {
	// Simulates the membership watcher updating the count at runtime: a sharding
	// that starts disabled (count 1) becomes a live shard once membership
	// observes replicas > 1.
	s := NewSharding(1, 1)
	if !s.Disabled() {
		t.Fatalf("precondition: NewSharding(1,1) should be disabled")
	}
	if !s.Owns("ns", "repo-x") {
		t.Error("disabled sharding should own everything")
	}
	s.SetNumShards(4)
	if s.NumShards() != 4 {
		t.Errorf("SetNumShards: NumShards() = %d, want 4", s.NumShards())
	}
	if s.Disabled() {
		t.Error("count 4 should not be disabled")
	}
}

func TestResolveShardID(t *testing.T) {
	tests := []struct {
		name    string
		podName string
		want    int
		wantErr bool
	}{
		{"derive from ordinal", "porch-controllers-2", 2, false},
		{"derive ordinal 0", "porch-controllers-0", 0, false},
		{"no ordinal (Deployment pod) returns 0", "porch-controllers-7x9k2", 0, false},
		{"non-numeric ordinal (Deployment pod) returns 0", "porch-controllers-x", 0, false},
		{"empty pod name returns 0", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveShardID(tt.podName)
			if err != nil {
				t.Fatalf("ResolveShardID() error = %v, wantErr false", err)
			}
			if got != tt.want {
				t.Errorf("ResolveShardID() = %d, want %d", got, tt.want)
			}
		})
	}
}
