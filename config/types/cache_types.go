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

import "github.com/kptdev/porch/config/types/datasize"

// GitCacheOptions are the go-git cache settings shared by porch-server and the
// repository controller.
//
// TODO: move to global. Server and controller should share one value.
type GitCacheOptions struct {
	GoGitRepoCacheSize int `flag:"gogit-repo-cache-size" default:"8" usage:"Size of the in-memory cache for git repositories when using gogit (in MiB)"`
	// 512KiB is the current flag default, 512 * datasize.KiB.
	GoGitCacheMaxFileSize datasize.DataSize `flag:"gogit-cache-max-file-size" default:"512KiB" usage:"Maximum file size, for example 512KB or a number of bytes, that will be read into the in-memory cache for git repositories when using gogit; files larger than this will be streamed from disk to avoid memory pressure"`
}

// DBCacheOptions are the database cache settings shared by porch-server and
// the repository controller.
//
// TODO: move to global. Server and controller should share one value.
type DBCacheOptions struct {
	Driver             string   `flag:"db-cache-driver" env:"DB_DRIVER" default:"pgx" usage:"Database driver to use when for the database cache" json:",omitempty"`
	DataSource         string   `flag:"db-cache-data-source" usage:"Address of the database, for example \"postgresql://user:pass@hostname:port/database\"" json:",omitempty"`
	MaxConnections     int      `flag:"db-max-connections" env:"DB_MAX_CONNECTIONS" default:"300" usage:"Maximum number of open connections in the database pool" json:",omitempty"`
	MaxIdleConnections int      `flag:"db-max-idle-connections" env:"DB_MAX_IDLE_CONNECTIONS" default:"100" usage:"Maximum number of idle connections in the database pool" json:",omitempty"`
	MaxConnLifetime    Duration `flag:"db-max-conn-lifetime" env:"DB_MAX_CONN_LIFETIME" default:"3m" usage:"Maximum lifetime of a database connection" json:",omitzero"`
}

type CRCacheOptions struct {
	MaxConcurrentLists       int      `flag:"max-parallel-repo-lists" default:"10" usage:"Maximum number of repositories to list in parallel." json:",omitempty"`
	ListTimeoutPerRepository Duration `flag:"list-timeout-per-repo" default:"20s" usage:"Maximum amount of time to wait for a repository list request." json:",omitzero"`
}
