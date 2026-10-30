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

// WrapperServer mirrors the flag fields of the options struct in func/wrapper-server.
// That struct is unexported and lives in package main.
// The command after "--" is the function entrypoint for one pod, not process configuration.
type WrapperServer struct {
	Port       int `default:"9446"`
	LogLevel   int `flag:"verbosity" default:"2"`
	FlattenLog bool
}
