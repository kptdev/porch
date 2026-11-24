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

// Package types holds the process configuration types for the Porch binaries.
package types

import "github.com/kptdev/porch/config/types/datasize"

// Config is the configuration for every porch component.
type Config struct {
	Global         Global
	Server         Server
	Controllers    Controllers
	FunctionRunner FunctionRunner
}

// Global holds settings that are not owned by a single component.
type Global struct {
	// DefaultImagePrefix is the prefix for unqualified function names.
	DefaultImagePrefix string `flag:"default-image-prefix" env:"*" default:"ghcr.io/kptdev/krm-functions-catalog" usage:"Default prefix for unqualified function names"`
	// MaxGrpcMessageSize is the shared gRPC limit. porch-server also uses it as the
	// maximum HTTP request body. 6MiB is 6 * datasize.MiB.
	MaxGrpcMessageSize datasize.DataSize `flag:"max-request-body-size" default:"6MiB" usage:"Maximum size of the request body and gRPC messages, for example 6MB or a number of bytes"`

	LifecyclePolicy LifecycleOptions `json:",omitzero" flagPrefix:""`

	Telemetry Telemetry
}
