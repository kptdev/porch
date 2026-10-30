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
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"github.com/kptdev/porch/config/types"
	"golang.org/x/tools/go/packages"
)

const schemaDraft = "https://json-schema.org/draft/2020-12/schema"

// Schema returns a JSON Schema (draft 2020-12) for dst.
// Property names are the file keys Overlay reads. No property is required.
// A default tag is copied as the schema default. Duration and DataSize are
// strings. Descriptions come from the Go field comment, plus the flag and
// environment variable when the field has them.
//
// types.Config is the master document, but each binary binds its own
// component. Flag names in that schema are the ones the binary registers.
func Schema(dst any) ([]byte, error) {
	fields, err := schemaFields(dst)
	if err != nil {
		return nil, err
	}
	fieldComments, typeComments, err := loadComments()
	if err != nil {
		return nil, err
	}
	root := &schemaNode{
		draft:      schemaDraft,
		typ:        "object",
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
		root.props = append(root.props, schemaProp{name: field.fileKey, schema: child})
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

// schemaFields is fieldsOf, except each component of types.Config is walked
// as its own bind root so flag names are not prefixed with global. or server.
func schemaFields(dst any) ([]fieldNode, error) {
	fields, err := fieldsOf(dst)
	if err != nil {
		return nil, err
	}
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.Elem().Type() != reflect.TypeFor[types.Config]() {
		return fields, nil
	}
	for i := range fields {
		if fields[i].leaf {
			continue
		}
		children, err := fieldsOf(fields[i].value.Addr().Interface())
		if err != nil {
			return nil, err
		}
		fields[i].children = children
	}
	return fields, nil
}

func nodeSchema(node fieldNode, fieldComments, typeComments map[string]string) (*schemaNode, error) {
	schema := &schemaNode{description: describe(node, fieldComments, typeComments)}
	if !node.leaf {
		schema.typ = "object"
		schema.additional = forbidExtra()
		schema.props = make([]schemaProp, 0, len(node.children))
		for _, child := range node.children {
			childSchema, err := nodeSchema(child, fieldComments, typeComments)
			if err != nil {
				return nil, err
			}
			schema.props = append(schema.props, schemaProp{name: child.fileKey, schema: childSchema})
		}
		return schema, nil
	}
	def, err := schemaDefault(node)
	if err != nil {
		return nil, err
	}
	schema.def = def
	switch node.value.Type() {
	case reflect.TypeFor[string](), reflect.TypeFor[types.DataSize](), reflect.TypeFor[types.Duration]():
		schema.typ = "string"
	case reflect.TypeFor[bool]():
		schema.typ = "boolean"
	case reflect.TypeFor[int](), reflect.TypeFor[int64]():
		schema.typ = "integer"
	case reflect.TypeFor[[]string]():
		schema.typ = "array"
		schema.items = &schemaNode{typ: "string"}
	default:
		return nil, fmt.Errorf("config field %s: unsupported type %s", node.path, node.value.Type())
	}
	return schema, nil
}

func forbidExtra() *bool {
	v := false
	return &v
}

func describe(node fieldNode, fieldComments, typeComments map[string]string) string {
	text := fieldComments[node.owner+"."+node.goName]
	if text == "" && !node.leaf {
		text = typeComments[node.value.Type().Name()]
	}
	var refs []string
	if node.leaf && node.flagName != "" {
		refs = append(refs, "--"+node.flagName)
	}
	if node.envName != "" {
		refs = append(refs, node.envName)
	}
	ref := strings.Join(refs, ", ")
	switch {
	case text == "":
		return ref
	case ref == "":
		return text
	default:
		return text + " " + ref
	}
}

func schemaDefault(node fieldNode) (json.RawMessage, error) {
	if !node.hasDef {
		return nil, nil
	}
	var value any
	switch node.value.Type() {
	case reflect.TypeFor[bool]():
		parsed, err := strconv.ParseBool(node.def)
		if err != nil {
			return nil, fmt.Errorf("config field %s: cannot parse default %q: %w", node.path, node.def, err)
		}
		value = parsed
	case reflect.TypeFor[int](), reflect.TypeFor[int64]():
		parsed, err := strconv.ParseInt(node.def, 10, node.value.Type().Bits())
		if err != nil {
			return nil, fmt.Errorf("config field %s: cannot parse default %q: %w", node.path, node.def, err)
		}
		value = parsed
	case reflect.TypeFor[[]string]():
		value = splitList(node.def)
	default:
		value = node.def
	}
	return json.Marshal(value)
}

type schemaNode struct {
	draft       string
	typ         string
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
	entries = append(entries, schemaEntry{key: "type", val: s.typ})
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
		return nil, nil, fmt.Errorf("config schema: cannot locate loader source")
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
	return strings.Join(strings.Fields(group.Text()), " ")
}
