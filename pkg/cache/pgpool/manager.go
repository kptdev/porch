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
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/klog/v2"
)

var (
	globalPool *pgxpool.Pool
	poolOnce   sync.Once
	poolErr    error
)

// Get returns the singleton pgxpool for this process.
// Each pod/process gets one pool; sharded controllers each get their own via separate init.
// Not a replacement for dbcache; a clean path for new components (PRRStore, future stores).
// Safe to call multiple times; returns the same pool.
func Get(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
	poolOnce.Do(func() {
		globalPool, poolErr = pgxpool.NewWithConfig(ctx, config)
		if poolErr == nil {
			klog.Infof("pgpool manager initialized with config: MaxConns=%d, MinConns=%d",
				config.MaxConns, config.MinConns)
		}
	})

	if poolErr != nil {
		return nil, fmt.Errorf("pgpool not initialized: %w", poolErr)
	}

	return globalPool, nil
}

// Close shuts down the pool. Called on process exit or graceful shutdown.
func Close() error {
	if globalPool == nil {
		klog.V(4).Infof("pgpool not initialized; nothing to close")
		return nil
	}

	globalPool.Close()
	klog.Infof("pgpool closed")
	return nil
}
