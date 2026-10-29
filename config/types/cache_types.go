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

package types

// TODO: remove the original cachetypes.DBCacheOptions in pkg/cache/types.
type DBCacheOptions struct {
	Driver             string   `flag:"db-cache-driver" env:"DB_DRIVER" default:"pgx"`
	DataSource         string   `flag:"db-cache-data-source"`
	MaxConnections     int      `env:"DB_MAX_CONNECTIONS" default:"300"`
	MaxIdleConnections int      `env:"DB_MAX_IDLE_CONNECTIONS" default:"100"`
	MaxConnLifetime    Duration `env:"DB_MAX_CONN_LIFETIME" default:"3m"`
}

// TODO: remove the original cachetypes.CRCacheOptions in pkg/cache/types.
type CRCacheOptions struct {
	MaxConcurrentLists       int      `flag:"max-parallel-repo-lists" default:"10"`
	ListTimeoutPerRepository Duration `flag:"list-timeout-per-repo" default:"20s"`
}
