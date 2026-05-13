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

package repomap

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kptdev/porch/pkg/repository"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
)

func TestLoadOrCreate_Success(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	repo, err := m.LoadOrCreate(key, func() (repository.Repository, error) {
		mock := &mockrepository.MockRepository{}
		mock.On("KubeObjectName").Return("created")
		return mock, nil
	})

	assert.NoError(t, err)
	assert.NotNil(t, repo)
}

func TestLoadOrCreate_Reuse(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	var callCount int32
	create := func() (repository.Repository, error) {
		atomic.AddInt32(&callCount, 1)
		return &mockrepository.MockRepository{}, nil
	}

	repo1, err1 := m.LoadOrCreate(key, create)
	repo2, err2 := m.LoadOrCreate(key, create)

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.Same(t, repo1, repo2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

func TestLoadOrCreate_ErrorRetry(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	var callCount int32
	create := func() (repository.Repository, error) {
		count := atomic.AddInt32(&callCount, 1)
		if count == 1 {
			return nil, errors.New("first attempt failed")
		}
		return &mockrepository.MockRepository{}, nil
	}

	// First call should fail
	repo1, err1 := m.LoadOrCreate(key, create)
	assert.Error(t, err1)
	assert.Nil(t, repo1)

	// Second call should succeed (retry)
	repo2, err2 := m.LoadOrCreate(key, create)
	assert.NoError(t, err2)
	assert.NotNil(t, repo2)
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
}

func TestLoadOrCreate_Concurrent(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	var callCount int32
	create := func() (repository.Repository, error) {
		atomic.AddInt32(&callCount, 1)
		return &mockrepository.MockRepository{}, nil
	}

	const goroutines = 10
	var wg sync.WaitGroup
	results := make([]repository.Repository, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			repo, err := m.LoadOrCreate(key, create)
			assert.NoError(t, err)
			results[idx] = repo
		}(i)
	}

	wg.Wait()

	// All goroutines should get the same instance
	for i := 1; i < goroutines; i++ {
		assert.Same(t, results[0], results[i])
	}

	// Create should only be called once
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

func TestLoadOrCreate_ConcurrentError(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	var callCount int32
	create := func() (repository.Repository, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, errors.New("always fails")
	}

	const goroutines = 5
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.LoadOrCreate(key, create)
			assert.Error(t, err)
		}()
	}

	wg.Wait()

	// With concurrent errors and deletion, multiple goroutines may call create
	// This is expected behavior - failed entries are deleted and retried
	count := atomic.LoadInt32(&callCount)
	assert.GreaterOrEqual(t, count, int32(1))
	assert.LessOrEqual(t, count, int32(goroutines))
}

func TestLoad(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	// Load non-existent key
	repo, ok := m.Load(key)
	assert.False(t, ok)
	assert.Nil(t, repo)

	// Create a repo
	m.LoadOrCreate(key, func() (repository.Repository, error) {
		return &mockrepository.MockRepository{}, nil
	})

	// Load existing key
	repo, ok = m.Load(key)
	assert.True(t, ok)
	assert.NotNil(t, repo)
}

func TestLoadAndDelete(t *testing.T) {
	m := &SafeRepoMap{}
	key := repository.RepositoryKey{Name: "test-repo"}

	// LoadAndDelete non-existent key
	repo, ok := m.LoadAndDelete(key)
	assert.False(t, ok)
	assert.Nil(t, repo)

	// Create a repo
	m.LoadOrCreate(key, func() (repository.Repository, error) {
		return &mockrepository.MockRepository{}, nil
	})

	// LoadAndDelete existing key
	repo, ok = m.LoadAndDelete(key)
	assert.True(t, ok)
	assert.NotNil(t, repo)

	// Verify it's deleted
	repo, ok = m.Load(key)
	assert.False(t, ok)
}

func TestRange(t *testing.T) {
	m := &SafeRepoMap{}

	// Create multiple repos
	keys := []repository.RepositoryKey{
		{Name: "repo1"},
		{Name: "repo2"},
		{Name: "repo3"},
	}

	for _, key := range keys {
		m.LoadOrCreate(key, func() (repository.Repository, error) {
			return &mockrepository.MockRepository{}, nil
		})
	}

	// Range over all entries and verify values are repositories, not loaders
	var count int
	m.Range(func(key, value any) bool {
		count++
		_, ok := value.(repository.Repository)
		assert.True(t, ok, "value should be a repository.Repository, not a repoLoader")
		return true
	})

	assert.Equal(t, len(keys), count)

	// Test early termination
	count = 0
	m.Range(func(key, value any) bool {
		count++
		return count < 2 // Stop after 2 iterations
	})

	assert.Equal(t, 2, count)
}
