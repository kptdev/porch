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

// FunctionRunner mirrors the options struct in func/server.
// That struct is unexported and lives in package main.
type FunctionRunner struct {
	Port             int    `default:"9445" usage:"The server port"`
	DisableRuntimes  string `usage:"The runtime(s) to disable. Multiple runtimes should separated by commas"`
	LogLevel         int    `default:"2" usage:"The verbosity level of the logs (0-5)"`
	FunctionCacheDir string `flag:"functions" default:"/home/nonroot/functions" usage:"Path to cached functions."`
}
