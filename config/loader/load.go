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

// Package loader fills config structs from defaults, a YAML or JSON document,
// environment variables, and command-line flags.
//
// A binary binds its own component struct, parses the process flag set, then
// overlays one section of the master document. Do not bind types.Config:
// that prefixes porch-server flags with "server.".
//
//	var cfg types.Server
//	if err := loader.Bind(cmd.Flags(), &cfg); err != nil { ... }
//	// Register the flags this struct does not own (klog, generic apiserver),
//	// then parse. Read the config path from that parse.
//	section, err := loader.Section(doc, "server")
//	if err := loader.Overlay(cmd.Flags(), &cfg, section); err != nil { ... }
//
// Every binary also binds types.Global and overlays Section(doc, "global").
// defaultImagePrefix and maxGrpcMessageSize live there, so
// --default-image-prefix and --max-request-body-size stay on the process
// flag set. porch-server binds types.Server and reads the "server" section.
// RecommendedOptions already registers --kubeconfig, --secure-port,
// --cert-dir, --enable-admission-plugins, and --disable-admission-plugins.
// Bind registers those too, so the server must not register them twice.
// Copy the parsed serving and kubeconfig fields into RecommendedOptions
// after Overlay. Building the database DSN and filling an empty cache
// directory stay in the server's Complete path.
//
// porch-controllers binds types.Controllers and reads "controllers".
// Add klog with FlagSet.AddGoFlagSet. The webhook cert directory and the
// reconciler list come from the bound struct. Telemetry is in the global
// section above.
//
// function-runner binds types.FunctionRunner and reads "functionRunner".
// wrapper-server binds types.WrapperServer and reads "wrapperServer".
// The command after "--" is cmd.Flags().Args(). Load drops those arguments,
// so the wrapper must use Bind and Overlay instead.
package loader

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

// Load fills dst from yamlOrJSON, the environment, and args.
// dst must be a non-nil pointer to a struct. A field with no tags uses a
// camelCase file key and a flag name derived from the field and its parents.
// Nested fields are joined with dots and words within a field with hyphens,
// so Controllers.Repository.CacheType is controllers.repository.cache-type.
// The zero value is the default unless the field has a default tag.
//
// A flagPrefix tag on a struct field replaces the derived flag path of its
// descendants. The loader joins it with a dot, so "repositories" yields
// repositories.cache-type. A flag tag replaces that field's own segment
// and still receives the parent prefix, so flag:"gogit-repo-cache-size"
// under that prefix is repositories.gogit-repo-cache-size.
//
// Environment variables are read only when the field has an env tag.
// env:"*" derives the name from that field alone, ignoring parents, so
// PodEvaluatorAddress becomes POD_EVALUATOR_ADDRESS. Any other value is
// the variable name.
//
// Precedence is the default, then keys present in the file, then an
// environment variable that is set, then a flag present in args.
// Load parses a private flag set and rejects flags it does not own.
// Binaries that share their flag set with other packages use Bind and Overlay.
func Load(dst any, yamlOrJSON []byte, args []string) error {
	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := Bind(flags, dst); err != nil {
		return err
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	return Overlay(flags, dst, yamlOrJSON)
}

// ReadFile reads a config file. An empty path means there is no file.
func ReadFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return contents, nil
}

// OverlaySection overlays the nested value at keys. A missing key leaves dst unchanged
// apart from the environment and the flags fs reports as changed.
func OverlaySection(fs *pflag.FlagSet, dst any, doc []byte, keys ...string) error {
	section, err := Section(doc, keys...)
	if err != nil {
		return err
	}
	return Overlay(fs, dst, section)
}
