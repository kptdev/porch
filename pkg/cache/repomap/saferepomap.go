// Copyright 2022, 2024-2025 The kpt Authors
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

// Package repomap provides a thread safe map of repositories for caches
package repomap

import (
	"sync"

	"github.com/kptdev/porch/pkg/repository"
)

type SafeRepoMap struct {
	syncMap sync.Map
}

func (s *SafeRepoMap) Load(key repository.RepositoryKey) (repository.Repository, bool) {
	v, ok := s.syncMap.Load(key)
	if !ok {
		return nil, false
	}
	return v.(*repoLoader).repo, true
}

func (s *SafeRepoMap) LoadAndDelete(key repository.RepositoryKey) (repository.Repository, bool) {
	v, ok := s.syncMap.LoadAndDelete(key)
	if !ok {
		return nil, false
	}
	return v.(*repoLoader).repo, true
}

func (s *SafeRepoMap) Range(f func(key, value any) bool) {
	s.syncMap.Range(func(key, value any) bool {
		loader := value.(*repoLoader)
		// Skip nil loaders (defensive) or repos that failed creation or haven't completed initialization
		if loader == nil || loader.repo == nil {
			return true
		}
		return f(key, loader.repo)
	})
}

type repoLoader struct {
	once sync.Once
	repo repository.Repository
	err  error
}

func (s *SafeRepoMap) LoadOrCreate(key repository.RepositoryKey, create func() (repository.Repository, error)) (repository.Repository, error) {
	loader := &repoLoader{}
	actual, loaded := s.syncMap.LoadOrStore(key, loader)
	l := actual.(*repoLoader)

	l.once.Do(func() {
		l.repo, l.err = create()
		if l.err != nil && !loaded {
			// Remove failed entry only if this thread created it, so subsequent calls can retry
			s.syncMap.Delete(key)
		}
	})

	return l.repo, l.err
}
