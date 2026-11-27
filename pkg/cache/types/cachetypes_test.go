// Copyright 2023.2025 The kpt Authors
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

package cachetypes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsACacheType(t *testing.T) {
	assert.True(t, IsACacheType("CR"))
	assert.True(t, IsACacheType("cr"))
	assert.True(t, IsACacheType("DB"))
	assert.False(t, IsACacheType(""))
	assert.False(t, IsACacheType("ZZ"))
}

func TestPRRStorePoolMaxConns(t *testing.T) {
	for _, tt := range []struct {
		maxConns int
		want     int32
	}{
		{maxConns: 0, want: 0},    // unlimited: keep the pgxpool default
		{maxConns: -1, want: 0},   // unlimited
		{maxConns: 2, want: 1},    // never below one connection
		{maxConns: 50, want: 12},  // controller default
		{maxConns: 300, want: 75}, // porch-server default
	} {
		assert.Equal(t, tt.want, DBCacheOptions{MaxConnections: tt.maxConns}.PRRStorePoolMaxConns(), "MaxConnections=%d", tt.maxConns)
	}
}
