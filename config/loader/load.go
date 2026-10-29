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
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/kptdev/porch/config/types"
	"github.com/spf13/pflag"
	"sigs.k8s.io/yaml"
)

// Load fills dst from yamlOrJSON, the environment, and args.
// dst must be a non-nil pointer to a struct. A field with no tags uses a
// camelCase file key, a kebab-case flag name, and an upper snake-case
// environment variable, all derived from the field name and its parents.
// The zero value is the default unless the field has a default tag.
//
// Precedence is the default, then keys present in the file, then an
// environment variable that is set, then a flag present in args.
func Load(dst any, yamlOrJSON []byte, args []string) error {
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config destination must be a non-nil pointer to a struct")
	}
	fields, err := structFields(value.Elem(), nil, nil, "", map[string]string{}, map[string]string{})
	if err != nil {
		return err
	}
	if err := applyDefaults(fields); err != nil {
		return err
	}
	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := registerFlags(flags, fields); err != nil {
		return err
	}
	raw, err := parseDocument(yamlOrJSON)
	if err != nil {
		return err
	}
	if raw != nil {
		if err := overlay(fields, raw, ""); err != nil {
			return err
		}
	}
	if err := applyEnv(fields, os.LookupEnv); err != nil {
		return err
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	return nil
}

type fieldNode struct {
	value    reflect.Value
	fileKey  string
	flagName string
	envName  string
	usage    string
	path     string
	def      string
	hasDef   bool
	leaf     bool
	children []fieldNode
}

func structFields(parent reflect.Value, flagParts, envParts []string, path string, seenFlag, seenEnv map[string]string) ([]fieldNode, error) {
	structType := parent.Type()
	nodes := make([]fieldNode, 0, structType.NumField())
	seenFile := map[string]string{}
	for i := range structType.NumField() {
		sf := structType.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		node, skip, err := newField(parent.Field(i), sf, flagParts, envParts, path, seenFlag, seenEnv)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if prev, ok := seenFile[node.fileKey]; ok {
			return nil, fmt.Errorf("config fields %s and %s share file key %q", prev, node.path, node.fileKey)
		}
		seenFile[node.fileKey] = node.path
		if node.leaf {
			if err := claimName(seenFlag, node.flagName, node.path, "flag"); err != nil {
				return nil, err
			}
			if err := claimName(seenEnv, node.envName, node.path, "environment variable"); err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func newField(value reflect.Value, sf reflect.StructField, flagParts, envParts []string, parentPath string, seenFlag, seenEnv map[string]string) (fieldNode, bool, error) {
	if skipped(sf.Tag) {
		return fieldNode{}, true, nil
	}
	words := splitWords(sf.Name)
	fileKey := camelCase(words)
	if name, ok := fileOverride(sf.Tag); ok {
		fileKey = name
	}
	path := fileKey
	if parentPath != "" {
		path = parentPath + "." + fileKey
	}
	flagName, envName := pathNames(sf.Tag, words, flagParts, envParts)
	node := fieldNode{
		value:    value,
		fileKey:  fileKey,
		flagName: flagName,
		envName:  envName,
		usage:    sf.Tag.Get("usage"),
		path:     path,
	}
	if def, ok := sf.Tag.Lookup("default"); ok {
		node.hasDef = true
		node.def = def
	}
	if isLeaf(sf.Type) {
		if !supported(sf.Type) {
			return fieldNode{}, false, fmt.Errorf("config field %s: unsupported type %s", path, sf.Type)
		}
		node.leaf = true
		return node, false, nil
	}
	children, err := structFields(value, appendCopy(flagParts, kebabCase(words)), appendCopy(envParts, envCase(words)), path, seenFlag, seenEnv)
	if err != nil {
		return fieldNode{}, false, err
	}
	node.children = children
	return node, false, nil
}

func skipped(tag reflect.StructTag) bool {
	for _, key := range []string{"flag", "json", "yaml", "env"} {
		if value, ok := tag.Lookup(key); ok {
			if _, skip := tagName(value); skip {
				return true
			}
		}
	}
	return false
}

// fileOverride returns the yaml tag when set, otherwise the json tag.
func fileOverride(tag reflect.StructTag) (string, bool) {
	for _, key := range []string{"yaml", "json"} {
		value, ok := tag.Lookup(key)
		if !ok {
			continue
		}
		name, skip := tagName(value)
		if skip || name == "" {
			continue
		}
		return name, true
	}
	return "", false
}

func pathNames(tag reflect.StructTag, words, flagParts, envParts []string) (flagName, envName string) {
	if name, skip := tagName(tag.Get("flag")); !skip && name != "" {
		flagName = name
	} else {
		flagName = joinParts(append(flagParts, kebabCase(words)), "-")
	}
	if name, skip := tagName(tag.Get("env")); !skip && name != "" {
		envName = name
	} else {
		envName = joinParts(append(envParts, envCase(words)), "_")
	}
	return flagName, envName
}

func joinParts(parts []string, sep string) string {
	return strings.Join(parts, sep)
}

func appendCopy(parts []string, part string) []string {
	out := make([]string, len(parts)+1)
	copy(out, parts)
	out[len(parts)] = part
	return out
}

func claimName(seen map[string]string, name, path, kind string) error {
	if prev, ok := seen[name]; ok {
		return fmt.Errorf("config fields %s and %s share %s %q", prev, path, kind, name)
	}
	seen[name] = path
	return nil
}

func isLeaf(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return true
	}
	return reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]())
}

func supported(t reflect.Type) bool {
	switch t {
	case reflect.TypeFor[string](), reflect.TypeFor[bool](), reflect.TypeFor[int](), reflect.TypeFor[int64](),
		reflect.TypeFor[types.DataSize](), reflect.TypeFor[types.Duration](), reflect.TypeFor[[]string]():
		return true
	default:
		return false
	}
}

func applyDefaults(fields []fieldNode) error {
	for i := range fields {
		node := &fields[i]
		if !node.leaf {
			if err := applyDefaults(node.children); err != nil {
				return err
			}
			continue
		}
		if !node.hasDef {
			continue
		}
		if err := assignString(node.value, node.def); err != nil {
			return fmt.Errorf("config field %s: cannot parse default %q: %w", node.path, node.def, err)
		}
	}
	return nil
}

func registerFlags(flags *pflag.FlagSet, fields []fieldNode) error {
	for i := range fields {
		node := &fields[i]
		if !node.leaf {
			if err := registerFlags(flags, node.children); err != nil {
				return err
			}
			continue
		}
		if flags.Lookup(node.flagName) != nil {
			return fmt.Errorf("config field %s: duplicate flag %q", node.path, node.flagName)
		}
		if err := registerFlag(flags, node); err != nil {
			return err
		}
	}
	return nil
}

func registerFlag(flags *pflag.FlagSet, node *fieldNode) error {
	switch node.value.Type() {
	case reflect.TypeFor[string]():
		flags.StringVar(node.value.Addr().Interface().(*string), node.flagName, node.value.String(), node.usage)
	case reflect.TypeFor[bool]():
		flags.BoolVar(node.value.Addr().Interface().(*bool), node.flagName, node.value.Bool(), node.usage)
	case reflect.TypeFor[int]():
		flags.IntVar(node.value.Addr().Interface().(*int), node.flagName, int(node.value.Int()), node.usage)
	case reflect.TypeFor[int64]():
		flags.Int64Var(node.value.Addr().Interface().(*int64), node.flagName, node.value.Int(), node.usage)
	case reflect.TypeFor[types.DataSize](), reflect.TypeFor[types.Duration](), reflect.TypeFor[[]string]():
		flags.Var(&scalarValue{value: node.value}, node.flagName, node.usage)
	default:
		return fmt.Errorf("config field %s: unsupported type %s", node.path, node.value.Type())
	}
	return nil
}

// scalarValue sets a Duration, DataSize, or string slice from a flag string.
type scalarValue struct {
	value reflect.Value
}

func (v *scalarValue) Set(raw string) error {
	return assignString(v.value, raw)
}

func (v *scalarValue) String() string {
	if v.value.Type() == reflect.TypeFor[[]string]() {
		return strings.Join(v.value.Interface().([]string), ",")
	}
	marshaler, ok := v.value.Interface().(json.Marshaler)
	if !ok {
		return ""
	}
	text, err := marshaler.MarshalJSON()
	if err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(text, &s); err != nil {
		return string(text)
	}
	return s
}

func (v *scalarValue) Type() string {
	return "string"
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

func overlay(fields []fieldNode, raw json.RawMessage, path string) error {
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
			if err := json.Unmarshal(childRaw, node.value.Addr().Interface()); err != nil {
				return fmt.Errorf("config field %s: cannot parse %s: %w", childPath, childRaw, err)
			}
			continue
		}
		if err := overlay(node.children, childRaw, childPath); err != nil {
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

func applyEnv(fields []fieldNode, lookup func(string) (string, bool)) error {
	for i := range fields {
		node := &fields[i]
		if !node.leaf {
			if err := applyEnv(node.children, lookup); err != nil {
				return err
			}
			continue
		}
		raw, ok := lookup(node.envName)
		if !ok {
			continue
		}
		if err := assignString(node.value, raw); err != nil {
			return fmt.Errorf("config field %s: cannot parse %q: %w", node.path, raw, err)
		}
	}
	return nil
}

func assignString(field reflect.Value, raw string) error {
	switch field.Type() {
	case reflect.TypeFor[string]():
		field.SetString(raw)
		return nil
	case reflect.TypeFor[bool]():
		parsed, err := strconv.ParseBool(raw)
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
	case reflect.TypeFor[types.DataSize](), reflect.TypeFor[types.Duration]():
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		return json.Unmarshal(encoded, field.Addr().Interface())
	case reflect.TypeFor[[]string]():
		field.Set(reflect.ValueOf(splitList(raw)))
		return nil
	default:
		return fmt.Errorf("unsupported type %s", field.Type())
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
