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
// There is no existing struct for these variables.
type DatabaseConnection struct {
	Host string `env:"DB_HOST" usage:"Database host"`
	Port string `env:"DB_PORT" usage:"Database port"`
	Name string `env:"DB_NAME" usage:"Database name"`
	User string `env:"DB_USER" usage:"Database user"`
	// Password belongs in a Secret, not the ConfigMap.
	Password string `env:"DB_PASSWORD" usage:"Database password. The value belongs in a Secret, not the ConfigMap"`
	SSLMode  string `env:"DB_SSL_MODE" usage:"Database SSL mode. Empty means disable"`
}
