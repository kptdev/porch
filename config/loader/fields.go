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
	"github.com/kptdev/porch/config/types/datasize"
)

func fieldsOf(dst any) ([]FieldNode, error) {
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("config destination must be a non-nil pointer to a struct")
	}
	return structFields(value.Elem(), "", "", map[string]string{})
}

// FieldNode is one node in the config field tree reflected from a struct.
type FieldNode struct {
	Value    reflect.Value
	FileKey  string
	FlagName string
	EnvName  string
	Usage    string
	Path     string
	Def      string
	Owner    string
	GoName   string
	HasDef   bool
	Leaf     bool
	Children []FieldNode
}

func structFields(parent reflect.Value, flagPrefix, path string, seenFlag map[string]string) ([]FieldNode, error) {
	structType := parent.Type()
	nodes := make([]FieldNode, 0, structType.NumField())
	seenFile := map[string]string{}
	for i := range structType.NumField() {
		sf := structType.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		if isEmbedded(sf) {
			inlined, err := structFields(parent.Field(i), flagPrefix, path, seenFlag)
			if err != nil {
				return nil, err
			}
			for _, child := range inlined {
				if prev, ok := seenFile[child.FileKey]; ok {
					return nil, fmt.Errorf("config fields %s and %s share file key %q", prev, child.Path, child.FileKey)
				}
				seenFile[child.FileKey] = child.Path
			}
			nodes = append(nodes, inlined...)
			continue
		}
		node, skip, err := newField(parent.Field(i), sf, flagPrefix, path, seenFlag)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		node.Owner = structType.Name()
		node.GoName = sf.Name
		if prev, ok := seenFile[node.FileKey]; ok {
			return nil, fmt.Errorf("config fields %s and %s share file key %q", prev, node.Path, node.FileKey)
		}
		seenFile[node.FileKey] = node.Path
		if node.Leaf && node.FlagName != "" {
			if err := claimName(seenFlag, node.FlagName, node.Path); err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// isEmbedded reports whether sf is an embedded struct whose fields belong to
// the parent: same file keys, same flag prefix. This lets a struct shared by
// several components be defined once. A tag that renames the field makes it an
// ordinary nested object.
func isEmbedded(sf reflect.StructField) bool {
	if !sf.Anonymous || sf.Type.Kind() != reflect.Struct || isLeaf(sf.Type) {
		return false
	}
	for _, key := range []string{"json", "yaml", "flag", "flagPrefix"} {
		if _, ok := sf.Tag.Lookup(key); ok {
			return false
		}
	}
	return true
}

func newField(value reflect.Value, sf reflect.StructField, flagPrefix, parentPath string, seenFlag map[string]string) (FieldNode, bool, error) {
	if skipped(sf.Tag) {
		return FieldNode{}, true, nil
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
	node := FieldNode{
		Value:    value,
		FileKey:  fileKey,
		FlagName: flagName,
		EnvName:  envName,
		Usage:    sf.Tag.Get("usage"),
		Path:     path,
	}
	if def, ok := sf.Tag.Lookup("default"); ok {
		node.HasDef = true
		node.Def = def
	}
	if isLeaf(sf.Type) {
		if !supported(sf.Type) {
			return FieldNode{}, false, fmt.Errorf("config field %s: unsupported type %s", path, sf.Type)
		}
		node.Leaf = true
		return node, false, nil
	}
	children, err := structFields(value, childFlagPrefix(sf.Tag, flagPrefix, words), path, seenFlag)
	if err != nil {
		return FieldNode{}, false, err
	}
	node.Children = children
	return node, false, nil
}

func skipped(tag reflect.StructTag) bool {
	for _, key := range []string{"json", "yaml", "env"} {
		if tagSkips(tag, key) {
			return true
		}
	}
	// flag:"-" drops a field that has nowhere else to live. An env tag keeps
	// the file key and the environment variable, and only the flag is omitted.
	if tagSkips(tag, "flag") {
		_, hasEnv := tag.Lookup("env")
		return !hasEnv
	}
	return false
}

func tagSkips(tag reflect.StructTag, key string) bool {
	value, ok := tag.Lookup(key)
	if !ok {
		return false
	}
	_, skip := tagName(value)
	return skip
}

// fileOverride returns the YAML tag when set, otherwise the json tag.
func fileOverride(tag reflect.StructTag) (string, bool) {
	for _, key := range []string{"yaml", "json"} {
		value, ok := tag.Lookup(key)
		if !ok {
			continue
		}
		name, _ := tagName(value)
		if name == "" {
			continue
		}
		return name, true
	}
	return "", false
}

func pathNames(tag reflect.StructTag, words []string, flagPrefix string) (flagName, envName string) {
	if _, skip := tagName(tag.Get("flag")); skip {
		flagName = ""
	} else {
		segment := kebabCase(words)
		if name, _ := tagName(tag.Get("flag")); name != "" {
			segment = name
		}
		flagName = joinFlag(flagPrefix, segment)
	}
	value, ok := tag.Lookup("env")
	if !ok {
		return flagName, ""
	}
	name, _ := tagName(value)
	if name == "" || name == "*" {
		return flagName, envCase(words)
	}
	return flagName, name
}

// childFlagPrefix is the flag path of fields inside a struct.
// flagPrefix replaces the derived path. Otherwise, the field's kebab-case
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

func claimName(seen map[string]string, name, path string) error {
	if prev, ok := seen[name]; ok {
		return fmt.Errorf("config fields %s and %s share flag %q", prev, path, name)
	}
	seen[name] = path
	return nil
}

func walk(fields []FieldNode, fn func(*FieldNode) error) error {
	for i := range fields {
		node := &fields[i]
		if !node.Leaf {
			if err := walk(node.Children, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(node); err != nil {
			return err
		}
	}
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
		reflect.TypeFor[datasize.DataSize](), reflect.TypeFor[types.Duration](), reflect.TypeFor[[]string]():
		return true
	default:
		return false
	}
}
