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

package pgpool

import (
	"context"
	"os/user"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/porch/pkg/cache/testutil"
)

// TestManager drives the pgpool singleton end-to-end against a real embedded postgres.
// The pool is a process-wide singleton guarded by sync.Once, so it can only be
// meaningfully initialized once per test binary. This single test therefore covers
// usability, Get idempotency, and Close rather than splitting across tests that would
// each try (and fail) to re-initialize the singleton.
//
// StartEmbeddedPostgres uses a dynamically-allocated port and per-test runtime/data
// dirs so this package runs in parallel with the other postgres test packages.
func TestManager(t *testing.T) {
	if u, err := user.Current(); err == nil && u.Username == "root" {
		t.Fatalf("This test cannot run as %q user", u.Username)
	}

	// Close is safe before any initialization (nil-pool guard).
	require.NoError(t, Close(), "Close must be a no-op before init")

	pg := testutil.StartEmbeddedPostgres(t)

	config, err := pgxpool.ParseConfig(pg.URL)
	require.NoError(t, err)
	config.MaxConns = 5
	config.MinConns = 1

	ctx := context.Background()

	// First Get initializes the singleton and returns a usable pool.
	pool, err := Get(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, pool)

	var result int
	require.NoError(t, pool.QueryRow(ctx, "SELECT 1").Scan(&result))
	require.Equal(t, 1, result)

	// Get is idempotent: repeated calls return the same singleton pool.
	again, err := Get(ctx, config)
	require.NoError(t, err)
	require.Same(t, pool, again, "Get must return the same singleton pool on repeated calls")

	// Close shuts the pool down.
	require.NoError(t, Close())
}
