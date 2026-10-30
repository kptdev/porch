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
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/kptdev/porch/config/types"
)

func fieldsOf(dst any) ([]fieldNode, error) {
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("config destination must be a non-nil pointer to a struct")
	}
	return structFields(value.Elem(), "", "", map[string]string{})
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

func structFields(parent reflect.Value, flagPrefix, path string, seenFlag map[string]string) ([]fieldNode, error) {
	structType := parent.Type()
	nodes := make([]fieldNode, 0, structType.NumField())
	seenFile := map[string]string{}
	for i := range structType.NumField() {
		sf := structType.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		node, skip, err := newField(parent.Field(i), sf, flagPrefix, path, seenFlag)
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
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func newField(value reflect.Value, sf reflect.StructField, flagPrefix, parentPath string, seenFlag map[string]string) (fieldNode, bool, error) {
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
	flagName, envName := pathNames(sf.Tag, words, flagPrefix)
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
	children, err := structFields(value, childFlagPrefix(sf.Tag, flagPrefix, words), path, seenFlag)
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

func pathNames(tag reflect.StructTag, words []string, flagPrefix string) (flagName, envName string) {
	segment := kebabCase(words)
	if name, skip := tagName(tag.Get("flag")); !skip && name != "" {
		segment = name
	}
	flagName = joinFlag(flagPrefix, segment)
	value, ok := tag.Lookup("env")
	if !ok {
		return flagName, ""
	}
	name, skip := tagName(value)
	if skip {
		return flagName, ""
	}
	if name == "" || name == "*" {
		return flagName, envCase(words)
	}
	return flagName, name
}

// childFlagPrefix is the flag path of fields inside a struct.
// flagPrefix replaces the derived path. Otherwise the field's kebab-case
// name is joined with a dot.
func childFlagPrefix(tag reflect.StructTag, parent string, words []string) string {
	if prefix, ok := tag.Lookup("flagPrefix"); ok {
		return prefix
	}
	return joinFlag(parent, kebabCase(words))
}

func joinFlag(prefix, segment string) string {
	if prefix == "" {
		return segment
	}
	if segment == "" {
		return prefix
	}
	return prefix + "." + segment
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
