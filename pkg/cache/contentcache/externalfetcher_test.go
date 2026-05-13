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

package contentcache

import (
	"context"
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewExternalPackageFetcher(t *testing.T) {
	f := NewExternalPackageFetcher(nil, nil, 3)
	require.NotNil(t, f)
}

func TestNewExternalPackageFetcherWithResolvers(t *testing.T) {
	f := NewExternalPackageFetcher(nil, nil, 5)
	impl := f.(*externalPackageFetcher)
	assert.Equal(t, 5, impl.repoOperationRetryAttempts)
	assert.Nil(t, impl.credentialResolver)
	assert.Nil(t, impl.caBundleResolver)
}

func TestFetchExternalGitPackage_InvalidRepo(t *testing.T) {
	f := NewExternalPackageFetcher(nil, nil, 1)

	gitSpec := &porchv1alpha2.GitPackage{
		Repo:      "not-a-valid-url",
		Ref:       "main",
		Directory: "pkg",
	}

	_, _, err := f.FetchExternalGitPackage(context.Background(), gitSpec, "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot clone git repository")
}
