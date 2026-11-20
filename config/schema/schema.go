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

// Package schemagen builds JSON Schema from config types. Runtime binaries
// should use loader only; import this package from codegen tools such as
// cmd/genschema.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"github.com/kptdev/porch/config/loader"
	"github.com/kptdev/porch/config/types"
	"github.com/kptdev/porch/config/types/datasize"
	"golang.org/x/tools/go/packages"
)

const schemaDraft = "https://json-schema.org/draft/2020-12/schema"

// Schema returns a JSON Schema (draft 2020-12) for dst.
// Property names are the file keys Overlay reads, none is required, default
// tags become schema defaults, and descriptions come from the Go field comments.
func Schema(dst any) ([]byte, error) {
	fields, err := loader.SchemaFields(dst)
	if err != nil {
		return nil, err
	}
	fieldComments, typeComments, err := loadComments()
	if err != nil {
		return nil, err
	}
	root := &schemaNode{
		draft:      schemaDraft,
		types:      []string{"object"},
		additional: forbidExtra(),
		props:      make([]schemaProp, 0, len(fields)),
	}
	if t := reflect.TypeOf(dst); t != nil && t.Kind() == reflect.Pointer {
		root.description = typeComments[t.Elem().Name()]
	}
	for _, field := range fields {
		child, err := nodeSchema(field, fieldComments, typeComments)
		if err != nil {
			return nil, err
		}
		root.props = append(root.props, schemaProp{name: field.FileKey, schema: child})
	}
	raw, err := root.marshal()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func nodeSchema(node loader.FieldNode, fieldComments, typeComments map[string]string) (*schemaNode, error) {
	schema := &schemaNode{description: describe(node, fieldComments, typeComments)}
	if !node.Leaf {
		schema.types = []string{"object"}
		schema.additional = forbidExtra()
		schema.props = make([]schemaProp, 0, len(node.Children))
		for _, child := range node.Children {
			childSchema, err := nodeSchema(child, fieldComments, typeComments)
			if err != nil {
				return nil, err
			}
			schema.props = append(schema.props, schemaProp{name: child.FileKey, schema: childSchema})
		}
		return schema, nil
	}
	def, err := schemaDefault(node)
	if err != nil {
		return nil, err
	}
	schema.def = def
	switch node.Value.Type() {
	case reflect.TypeFor[datasize.DataSize]():
		schema.types = []string{"string", "integer"}
	case reflect.TypeFor[string](), reflect.TypeFor[types.Duration]():
		schema.types = []string{"string"}
	case reflect.TypeFor[bool]():
		schema.types = []string{"boolean"}
	case reflect.TypeFor[int](), reflect.TypeFor[int64]():
		schema.types = []string{"integer"}
	case reflect.TypeFor[[]string]():
		schema.types = []string{"array"}
		schema.items = &schemaNode{types: []string{"string"}}
	default:
		return nil, fmt.Errorf("config field %s: unsupported type %s", node.Path, node.Value.Type())
	}
	return schema, nil
}

func forbidExtra() *bool {
	v := false
	return &v
}

func describe(node loader.FieldNode, fieldComments, typeComments map[string]string) string {
	text := fieldComments[node.Owner+"."+node.GoName]
	if text == "" && !node.Leaf {
		text = typeComments[node.Value.Type().Name()]
	}
	return text
}

func schemaDefault(node loader.FieldNode) (json.RawMessage, error) {
	if !node.HasDef {
		return nil, nil
	}
	var value any
	switch node.Value.Type() {
	case reflect.TypeFor[bool]():
		parsed, err := loader.ParseBool(node.Def)
		if err != nil {
			return nil, fmt.Errorf("config field %s: cannot parse default %q: %w", node.Path, node.Def, err)
		}
		value = parsed
	case reflect.TypeFor[int](), reflect.TypeFor[int64]():
		parsed, err := strconv.ParseInt(node.Def, 10, node.Value.Type().Bits())
		if err != nil {
			return nil, fmt.Errorf("config field %s: cannot parse default %q: %w", node.Path, node.Def, err)
		}
		value = parsed
	case reflect.TypeFor[[]string]():
		value = loader.SplitList(node.Def)
	default:
		value = node.Def
	}
	return json.Marshal(value)
}

type schemaNode struct {
	draft       string
	types       []string
	description string
	def         json.RawMessage
	items       *schemaNode
	props       []schemaProp
	additional  *bool
}

type schemaProp struct {
	name   string
	schema *schemaNode
}

func (s *schemaNode) marshal() ([]byte, error) {
	var entries []schemaEntry
	if s.draft != "" {
		entries = append(entries, schemaEntry{key: "$schema", val: s.draft})
	}
	if l := len(s.types); l == 1 {
		entries = append(entries, schemaEntry{key: "type", val: s.types[0]})
	} else if l > 1 {
		entries = append(entries, schemaEntry{key: "type", val: s.types})
	}
	if s.description != "" {
		entries = append(entries, schemaEntry{key: "description", val: s.description})
	}
	if len(s.def) > 0 {
		entries = append(entries, schemaEntry{key: "default", val: s.def})
	}
	if s.items != nil {
		raw, err := s.items.marshal()
		if err != nil {
			return nil, err
		}
		entries = append(entries, schemaEntry{key: "items", val: json.RawMessage(raw)})
	}
	if s.props != nil {
		props := make([]schemaEntry, 0, len(s.props))
		for _, prop := range s.props {
			raw, err := prop.schema.marshal()
			if err != nil {
				return nil, err
			}
			props = append(props, schemaEntry{key: prop.name, val: json.RawMessage(raw)})
		}
		raw, err := marshalEntries(props)
		if err != nil {
			return nil, err
		}
		entries = append(entries, schemaEntry{key: "properties", val: json.RawMessage(raw)})
	}
	if s.additional != nil {
		entries = append(entries, schemaEntry{key: "additionalProperties", val: *s.additional})
	}
	return marshalEntries(entries)
}

type schemaEntry struct {
	key string
	val any
}

func marshalEntries(entries []schemaEntry) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, entry := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(entry.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(entry.val)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func loadComments() (map[string]string, map[string]string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return nil, nil, fmt.Errorf("config schema: cannot locate schemagen source")
	}
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedSyntax | packages.NeedCompiledGoFiles,
		Dir:  filepath.Dir(file),
	}, "github.com/kptdev/porch/config/types")
	if err != nil {
		return nil, nil, fmt.Errorf("config schema: %w", err)
	}
	if len(loaded) != 1 {
		return nil, nil, fmt.Errorf("config schema: expected the types package")
	}
	if len(loaded[0].Errors) > 0 {
		return nil, nil, fmt.Errorf("config schema: %v", loaded[0].Errors[0])
	}
	fields, typeDocs := commentsIn(loaded[0].Syntax)
	return fields, typeDocs, nil
}

func commentsIn(files []*ast.File) (map[string]string, map[string]string) {
	fields := map[string]string{}
	typeDocs := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				doc := ts.Doc
				if doc == nil && len(gen.Specs) == 1 {
					doc = gen.Doc
				}
				if text := commentText(doc); text != "" {
					typeDocs[ts.Name.Name] = text
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					text := commentText(field.Doc)
					if text == "" {
						text = commentText(field.Comment)
					}
					if text == "" {
						continue
					}
					for _, name := range field.Names {
						fields[ts.Name.Name+"."+name.Name] = text
					}
				}
			}
		}
	}
	return fields, typeDocs
}

func commentText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	var kept []string
	for paragraph := range strings.SplitSeq(group.Text(), "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(paragraph), "TODO") {
			continue
		}
		kept = append(kept, strings.Join(strings.Fields(paragraph), " "))
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}
