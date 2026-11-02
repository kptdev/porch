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

package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/kptdev/porch/config/types"
	"github.com/spf13/pflag"
	"sigs.k8s.io/yaml"
)

// Overlay applies yamlOrJSON and then the environment onto dst.
// A flag fs reports as changed is left alone, so an explicit flag wins.
// fs must be the flag set passed to Bind, after it has been parsed.
func Overlay(fs *pflag.FlagSet, dst any, yamlOrJSON []byte) error {
	if fs == nil {
		return fmt.Errorf("config flag set is nil")
	}
	fields, err := fieldsOf(dst)
	if err != nil {
		return err
	}
	raw, err := parseDocument(yamlOrJSON)
	if err != nil {
		return err
	}
	changed := func(name string) bool { return fs.Changed(name) }
	if raw != nil {
		if err := overlay(fields, raw, "", changed); err != nil {
			return err
		}
	}
	return applyEnv(fields, os.LookupEnv, changed)
}

// Section returns the value at keys in a YAML or JSON document.
// keys are the file names of nested objects, such as "server" or
// "global", "telemetry". A missing key returns nil, nil.
func Section(doc []byte, keys ...string) ([]byte, error) {
	current, err := parseDocument(doc)
	if err != nil || current == nil {
		return nil, err
	}
	for _, key := range keys {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			return nil, fmt.Errorf("config field %s: %w", key, err)
		}
		next, ok := obj[key]
		if !ok || len(next) == 0 || string(next) == "null" {
			return nil, nil
		}
		current = next
	}
	return current, nil
}

func parseDocument(yamlOrJSON []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(yamlOrJSON)) == 0 {
		return nil, nil
	}
	converted, err := yaml.YAMLToJSON(yamlOrJSON)
	if err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	if len(converted) == 0 || string(converted) == "null" {
		return nil, nil
	}
	return converted, nil
}

func overlay(fields []fieldNode, raw json.RawMessage, path string, changed func(string) bool) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("config field %s: %w", pathOrRoot(path), err)
	}
	byKey := make(map[string]*fieldNode, len(fields))
	for i := range fields {
		byKey[fields[i].fileKey] = &fields[i]
	}
	for key, childRaw := range obj {
		node := byKey[key]
		if node == nil || len(childRaw) == 0 || string(childRaw) == "null" {
			continue
		}
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}
		if node.leaf {
			if node.flagName != "" && changed(node.flagName) {
				continue
			}
			if err := json.Unmarshal(childRaw, node.value.Addr().Interface()); err != nil {
				return fmt.Errorf("config field %s: cannot parse %s: %w", childPath, childRaw, err)
			}
			continue
		}
		if err := overlay(node.children, childRaw, childPath, changed); err != nil {
			return err
		}
	}
	return nil
}

func pathOrRoot(path string) string {
	if path == "" {
		return "document"
	}
	return path
}

func applyEnv(fields []fieldNode, lookup func(string) (string, bool), changed func(string) bool) error {
	return walk(fields, func(node *fieldNode) error {
		if node.envName == "" || (node.flagName != "" && changed(node.flagName)) {
			return nil
		}
		raw, ok := lookup(node.envName)
		if !ok {
			return nil
		}
		if err := assignString(node.value, raw); err != nil {
			return fmt.Errorf("config field %s: cannot parse %q: %w", node.path, raw, err)
		}
		return nil
	})
}

func assignString(field reflect.Value, raw string) error {
	switch field.Type() {
	case reflect.TypeFor[string]():
		field.SetString(raw)
		return nil
	case reflect.TypeFor[bool]():
		parsed, err := parseBool(raw)
		if err != nil {
			return err
		}
		field.SetBool(parsed)
		return nil
	case reflect.TypeFor[int](), reflect.TypeFor[int64]():
		parsed, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
		return nil
	case reflect.TypeFor[types.DataSize]():
		return field.Addr().Interface().(*types.DataSize).UnmarshalText([]byte(raw))
	case reflect.TypeFor[types.Duration]():
		return field.Addr().Interface().(*types.Duration).UnmarshalText([]byte(raw))
	case reflect.TypeFor[[]string]():
		field.Set(reflect.ValueOf(splitList(raw)))
		return nil
	default:
		return fmt.Errorf("unsupported type %s", field.Type())
	}
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(raw) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return strconv.ParseBool(raw)
	}
}

func splitList(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}
