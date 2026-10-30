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

package lgcache

import (
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/pkg/cache/lgcache/lock"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/require"
)

func TestPartitionRevisionKeys(t *testing.T) {
	a := key.PackageRevisionKey{WorkspaceName: "a"}
	b := key.PackageRevisionKey{WorkspaceName: "b"}
	c := key.PackageRevisionKey{WorkspaceName: "c"}
	left := map[key.PackageRevisionKey]repository.PackageRevision{
		a: nil,
		b: nil,
	}
	right := map[key.PackageRevisionKey]repository.PackageRevision{
		b: nil,
		c: nil,
	}
	leftOnly, both, rightOnly := partitionRevisionKeys(left, right)
	require.ElementsMatch(t, []key.PackageRevisionKey{a}, leftOnly)
	require.ElementsMatch(t, []key.PackageRevisionKey{b}, both)
	require.ElementsMatch(t, []key.PackageRevisionKey{c}, rightOnly)
}

func TestRepoLockExclusiveBlocks(t *testing.T) {
	path := t.TempDir() + "/repo.lock"
	unlock, err := lock.Lock(path, true, time.Second)
	require.NoError(t, err)
	_, err = lock.Lock(path, true, 40*time.Millisecond)
	require.Error(t, err)
	unlock()
	unlock2, err := lock.Lock(path, true, time.Second)
	require.NoError(t, err)
	unlock2()
}
