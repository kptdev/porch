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

package schema

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/kptdev/porch/config/loader"
	"github.com/kptdev/porch/config/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaMatchesCheckedInFile(t *testing.T) {
	got, err := Schema(&types.Config{})
	require.NoError(t, err)

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(file), "..", "generated", "porch.config.schema.json")
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestCommentsIn(t *testing.T) {
	const src = `package p

// Solo is alone.
type Solo struct{}

// Widget is a thing.
type Widget struct {
	// Name is the name.
	Name string
	Count int // inline count
}

type (
	// Other is grouped.
	Other struct {
		Value string
	}
)
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	require.NoError(t, err)
	fields, typeDocs := commentsIn([]*ast.File{file})

	assert.Equal(t, "Solo is alone.", typeDocs["Solo"])
	assert.Equal(t, "Widget is a thing.", typeDocs["Widget"])
	assert.Equal(t, "Other is grouped.", typeDocs["Other"])
	assert.Equal(t, "Name is the name.", fields["Widget.Name"])
	assert.Equal(t, "inline count", fields["Widget.Count"])
	assert.Empty(t, commentText(nil))
}

func TestCommentsInSkipsTodos(t *testing.T) {
	const src = `package p

type W struct {
	// TODO: single line
	OnlyTodo string

	// TODO: multi line
	//       continuation
	//       more
	MultiTodo string

	// Real description.
	//
	// TODO: trailing todo
	//       continued
	Mixed string

	// Before.
	//
	// TODO: middle
	//
	// After.
	Middle string

	Plain string // TODO: inline
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	require.NoError(t, err)
	fields, _ := commentsIn([]*ast.File{file})

	assert.NotContains(t, fields, "W.OnlyTodo")
	assert.NotContains(t, fields, "W.MultiTodo")
	assert.NotContains(t, fields, "W.Plain")
	assert.Equal(t, "Real description.", fields["W.Mixed"])
	assert.Equal(t, "Before. After.", fields["W.Middle"])
}

func TestLoadComments(t *testing.T) {
	fields, typeDocs, err := loadComments()
	require.NoError(t, err)
	assert.Contains(t, fields["DatabaseConnection.Password"], "Secret")
	assert.Contains(t, fields["DatabaseConnection.Password"], "ConfigMap")
	assert.NotEmpty(t, typeDocs["Config"])
}

func TestDescribe(t *testing.T) {
	assert.Equal(t, "cache kind", describe(loader.FieldNode{
		Owner: "Server", GoName: "CacheType", Leaf: true, FlagName: "cache-type", EnvName: "CACHE",
	}, map[string]string{"Server.CacheType": "cache kind"}, nil))

	assert.Empty(t, describe(loader.FieldNode{
		Leaf: true, FlagName: "kubeconfig",
	}, nil, nil))

	assert.Equal(t, "settings", describe(loader.FieldNode{
		Value: reflect.ValueOf(types.Global{}), Owner: "Config", GoName: "Global",
	}, nil, map[string]string{"Global": "settings"}))
}

func TestSchemaDefault(t *testing.T) {
	on := true
	count := 3
	items := []string{"a", "b"}
	tests := []struct {
		name    string
		node    loader.FieldNode
		want    string
		wantErr bool
	}{
		{name: "absent", node: loader.FieldNode{Value: reflect.ValueOf("")}},
		{name: "bool", node: loader.FieldNode{Value: reflect.ValueOf(on), HasDef: true, Def: "true", Path: "on"}, want: "true"},
		{name: "int", node: loader.FieldNode{Value: reflect.ValueOf(count), HasDef: true, Def: "3", Path: "count"}, want: "3"},
		{name: "list", node: loader.FieldNode{Value: reflect.ValueOf(items), HasDef: true, Def: "a, b", Path: "items"}, want: `["a","b"]`},
		{name: "text", node: loader.FieldNode{Value: reflect.ValueOf(types.Duration{}), HasDef: true, Def: "5m", Path: "wait"}, want: `"5m"`},
		{name: "bad bool", node: loader.FieldNode{Value: reflect.ValueOf(on), HasDef: true, Def: "nope", Path: "on"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := schemaDefault(tt.node)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestNodeSchema(t *testing.T) {
	schema, err := nodeSchema(loader.FieldNode{
		Value: reflect.ValueOf(types.Global{}),
		Children: []loader.FieldNode{{
			FileKey:  "name",
			Owner:    "Sample",
			GoName:   "Name",
			Leaf:     true,
			FlagName: "name",
			EnvName:  "NAME",
			HasDef:   true,
			Def:      "porch",
			Path:     "name",
			Value:    reflect.ValueOf(""),
		}},
	}, map[string]string{"Sample.Name": "the name"}, map[string]string{"Global": "global settings"})
	require.NoError(t, err)
	raw, err := schema.marshal()
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"type": "object",
		"description": "global settings",
		"properties": {
			"name": {
				"type": "string",
				"description": "the name",
				"default": "porch"
			}
		},
		"additionalProperties": false
	}`, string(raw))

	_, err = nodeSchema(loader.FieldNode{Leaf: true, Path: "ratio", Value: reflect.ValueOf(1.5)}, nil, nil)
	assert.Error(t, err)
}
