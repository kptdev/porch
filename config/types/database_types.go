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

// DatabaseConnection is the set of environment variables that
// PorchServerOptions.setupDBCacheConn and
// RepositoryReconciler.setupDBCacheOptionsFromEnv read and fold into
// DBCacheOptions.DataSource. Driver is that struct's Driver field.
type DatabaseConnection struct {
	Host string `flag:"-" env:"DB_HOST" json:",omitempty"`
	Port string `flag:"-" env:"DB_PORT" json:",omitempty"`
	Name string `flag:"-" env:"DB_NAME" json:",omitempty"`
	User string `flag:"-" env:"DB_USER" json:",omitempty"`
	// Password belongs in a Secret, not the ConfigMap.
	Password string `flag:"-" env:"DB_PASSWORD" json:",omitempty"`
	SSLMode  string `flag:"-" env:"DB_SSL_MODE" json:",omitempty"`
}
