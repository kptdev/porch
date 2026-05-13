// Copyright 2025 The kpt Authors
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
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-git/go-git/v5"
	"k8s.io/klog/v2"
)

// directoryPool manages shared access to cached git directories
type directoryPool struct {
	directories sync.Map
	mutex       sync.Mutex
}

type sharedDirectory struct {
	repo     *git.Repository
	mutex    sync.RWMutex
	refCount int // number of gitRepository instances using this directory
}

var globalDirectoryPool = &directoryPool{
	directories: sync.Map{},
}

// getOrCreateSharedRepository safely initializes or reuses a cached git directory
func (p *directoryPool) getOrCreateSharedRepository(dir, reponame string) (*sharedDirectory, error) {
	// Fast path: check if directory already exists
	if sharedDir, exists := p.directories.Load(dir); exists {
		p.mutex.Lock()
		shared := sharedDir.(*sharedDirectory)
		shared.refCount++
		klog.V(2).Infof("Repo %s is reusing shared directory %s, refCount now: %d", reponame, dir, shared.refCount)
		p.mutex.Unlock()
		return shared, nil
	}

	// Slow path: create new directory with write lock - needed to avoid race condition during parallel operations
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Double-check after acquiring lock
	if sharedDir, exists := p.directories.Load(dir); exists {
		shared := sharedDir.(*sharedDirectory)
		shared.refCount++
		klog.V(2).Infof("Repo %s is reusing shared directory %s, refCount now: %d", reponame, dir, shared.refCount)
		return shared, nil
	}

	klog.V(2).Infof("Repo %s initializing shared directory %s", reponame, dir)

	// Initialize repository safely
	var repo *git.Repository
	if fi, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		repo, err = initEmptyRepository(dir)
		if err != nil {
			if removeErr := os.RemoveAll(dir); removeErr != nil {
				klog.Errorf("Failed to remove partially created directory %s: %v", dir, removeErr)
			}
			return nil, err
		}
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("cache location %q is not a directory", dir)
	} else {
		repo, err = openRepository(dir)
		if err != nil {
			if removeErr := os.RemoveAll(dir); removeErr != nil {
				klog.Errorf("Failed to open repository %s: %v (also failed to remove corrupted directory: %v)", dir, err, removeErr)
				return nil, fmt.Errorf("open of cached git directory failed in gogit (check the local git cache): %w", err)
			}
			klog.Infof("Removed corrupted cached directory %s, will retry on next attempt", dir)
			return nil, fmt.Errorf("open of cached git directory failed in gogit (corrupted cache was removed): %w", err)
		}
	}

	shared := &sharedDirectory{
		repo:     repo,
		refCount: 1,
	}
	p.directories.Store(dir, shared)
	klog.V(2).Infof("Created new shared directory %s, refCount: %d", dir, shared.refCount)
	return shared, nil
}

// releaseSharedRepository decrements reference count and cleans up cached git directory if needed
func (p *directoryPool) releaseSharedRepository(dir, reponame string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if sharedDir, exists := p.directories.Load(dir); exists {
		shared := sharedDir.(*sharedDirectory)
		shared.refCount--
		klog.V(2).Infof("Released repo %s from %s, refCount now: %d", reponame, filepath.Base(dir), shared.refCount)

		if shared.refCount <= 0 {
			p.directories.Delete(dir)
			klog.Infof("Cleaning up cached directory %s (refCount reached 0)", filepath.Base(dir))
			if err := os.RemoveAll(dir); err != nil {
				klog.Errorf("Failed to remove cached directory %s: %v", dir, err)
			} else {
				klog.Infof("Successfully removed cached directory %s", dir)
			}
		}
	} else {
		// Directory not in pool, but try to clean up anyway in case of orphaned directories
		if _, err := os.Stat(dir); err == nil {
			if err := os.RemoveAll(dir); err == nil {
				klog.V(2).Infof("Cleaned up orphaned directory %s", dir)
			}
		}
	}
}

// withLock executes function with exclusive access to the cached git directory
func (s *sharedDirectory) withLock(fn func(*git.Repository) error) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return fn(s.repo)
}

// withRLock executes function with read-only access to the cached git directory
func (s *sharedDirectory) withRLock(fn func(*git.Repository) error) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return fn(s.repo)
}
