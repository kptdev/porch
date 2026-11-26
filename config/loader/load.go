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
// A binary binds its own component struct (and types.Global) to its flag set,
// parses the flags, then overlays the matching section of the master document.
// Do not bind types.Config itself: that would prefix every flag with the
// component name.
//
//	var cfg types.Server
//	if err := loader.Bind(cmd.Flags(), &cfg); err != nil { ... }
//	// register any other flags, then parse
//	if err := loader.OverlaySection(cmd.Flags(), &cfg, doc, "server"); err != nil { ... }
//
// Load does the same on a private flag set for callers that share nothing.
package loader

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

// Load fills dst, a non-nil pointer to a struct, from yamlOrJSON, the
// environment, and args. It parses a private flag set and rejects flags it
// does not own; binaries that share their flag set use Bind and Overlay.
//
// Field names map to a camelCase file key and a hyphenated flag name, with
// nested fields joined by dots (controllers.repository.cache-type). Struct tags
// adjust this: flag, flagPrefix, env ("*" derives the name from the field),
// default, and usage.
//
// Precedence is the default, then the file, then the environment, then flags.
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
