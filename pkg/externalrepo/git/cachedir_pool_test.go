// Copyright 2025-2026 The kpt Authors
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

package git

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoryPool_GetOrCreateSharedRepository(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "test-repo")

	// First call should create new shared directory
	shared1, err := pool.getOrCreateSharedRepository(repoDir, "test-repo")
	require.NoError(t, err)
	require.NotNil(t, shared1)
	assert.Equal(t, 1, shared1.refCount)

	// Second call should reuse existing directory
	shared2, err := pool.getOrCreateSharedRepository(repoDir, "test-repo")
	require.NoError(t, err)
	assert.Same(t, shared1, shared2)
	assert.Equal(t, 2, shared2.refCount)
}

func TestDirectoryPool_ReleaseSharedRepository(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "test-repo")

	// Create shared repository
	shared, err := pool.getOrCreateSharedRepository(repoDir, "test-repo")
	require.NoError(t, err)

	// Add another reference
	_, err = pool.getOrCreateSharedRepository(repoDir, "test-repo")
	require.NoError(t, err)

	// First release should decrement refCount
	pool.releaseSharedRepository(repoDir, "test-repo")
	assert.Equal(t, 1, shared.refCount)

	// Second release should remove from pool and cleanup directory
	pool.releaseSharedRepository(repoDir, "test-repo")
	_, exists := pool.directories.Load(repoDir)
	assert.False(t, exists)
	_, err = os.Stat(repoDir)
	assert.True(t, os.IsNotExist(err))
}

func TestSharedDirectory_WithLock(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := initEmptyRepository(tempDir)
	require.NoError(t, err)

	shared := &sharedDirectory{
		repo:     repo,
		refCount: 1,
	}

	called := false
	err = shared.withLock(func(r *git.Repository) error {
		called = true
		assert.Same(t, repo, r)
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSharedDirectory_WithRLock(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := initEmptyRepository(tempDir)
	require.NoError(t, err)

	shared := &sharedDirectory{
		repo:     repo,
		refCount: 1,
	}

	called := false
	err = shared.withRLock(func(r *git.Repository) error {
		called = true
		assert.Same(t, repo, r)
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestDirectoryPool_ConcurrentAccess(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "concurrent-repo")

	var wg sync.WaitGroup
	numGoroutines := 10

	// Concurrent creation
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := pool.getOrCreateSharedRepository(repoDir, "concurrent-repo")
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Check final refCount
	sharedDir, _ := pool.directories.Load(repoDir)
	shared := sharedDir.(*sharedDirectory)

	assert.Equal(t, numGoroutines, shared.refCount)

	// Concurrent release
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.releaseSharedRepository(repoDir, "concurrent-repo")
		}()
	}
	wg.Wait()

	// Directory should be cleaned up
	_, exists := pool.directories.Load(repoDir)
	assert.False(t, exists)
}

func TestDirectoryPool_InitEmptyRepositoryFailure(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	// Use a path that will fail (e.g., invalid permissions or read-only filesystem)
	// Create a file where we expect a directory
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "file-not-dir")
	err := os.WriteFile(filePath, []byte("test"), 0644)
	require.NoError(t, err)

	repoDir := filepath.Join(filePath, "subdir") // This will fail because parent is a file

	// Should fail to create repository
	_, err = pool.getOrCreateSharedRepository(repoDir, "test-repo")
	assert.Error(t, err)

	// Should not be in pool
	_, exists := pool.directories.Load(repoDir)
	assert.False(t, exists)

	// Retry should work if we fix the issue
	os.Remove(filePath)
	_, err = pool.getOrCreateSharedRepository(repoDir, "test-repo")
	assert.NoError(t, err)
}

func TestDirectoryPool_OpenRepositoryFailure(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "corrupted-repo")

	// Create a directory that looks like a git repo but is corrupted
	err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)
	require.NoError(t, err)
	// Write invalid git config
	err = os.WriteFile(filepath.Join(repoDir, ".git", "config"), []byte("invalid"), 0644)
	require.NoError(t, err)

	// Should fail to open corrupted repository
	_, err = pool.getOrCreateSharedRepository(repoDir, "test-repo")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "corrupted cache was removed")

	// Should not be in pool
	_, exists := pool.directories.Load(repoDir)
	assert.False(t, exists)

	// Directory should be cleaned up
	_, err = os.Stat(repoDir)
	assert.True(t, os.IsNotExist(err))

	// Retry should work now that corrupted dir is removed
	_, err = pool.getOrCreateSharedRepository(repoDir, "test-repo")
	assert.NoError(t, err)
}

func TestDirectoryPool_OpenRepositoryCleanupFailure(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "readonly-repo")

	// Create a corrupted git directory
	err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, ".git", "config"), []byte("invalid"), 0644)
	require.NoError(t, err)

	// Make directory read-only to cause RemoveAll to fail
	err = os.Chmod(repoDir, 0444)
	require.NoError(t, err)

	// Cleanup: restore permissions after test
	defer os.Chmod(repoDir, 0755)

	// Should fail to open and also fail to cleanup
	_, err = pool.getOrCreateSharedRepository(repoDir, "test-repo")
	assert.Error(t, err)
	// When cleanup fails, error message should mention checking local cache
	assert.Contains(t, err.Error(), "check the local git cache")

	// Should not be in pool
	_, exists := pool.directories.Load(repoDir)
	assert.False(t, exists)

	// Directory should still exist (cleanup failed)
	_, err = os.Stat(repoDir)
	assert.NoError(t, err)
}

func TestDirectoryPool_PathIsFile(t *testing.T) {
	pool := &directoryPool{
		directories: sync.Map{},
	}

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "not-a-directory")

	// Create a file instead of directory
	err := os.WriteFile(filePath, []byte("test"), 0644)
	require.NoError(t, err)

	// Should fail because path is a file, not a directory
	_, err = pool.getOrCreateSharedRepository(filePath, "test-repo")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")

	// Should not be in pool
	_, exists := pool.directories.Load(filePath)
	assert.False(t, exists)
}
