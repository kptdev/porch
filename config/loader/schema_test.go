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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/porch/config/types"
)

func TestSchemaMatchesCheckedInFile(t *testing.T) {
	got, err := Schema(&types.Config{})
	require.NoError(t, err)

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(file), "..", "schema", "porch.config.schema.json")
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

func TestLoadComments(t *testing.T) {
	fields, typeDocs, err := loadComments()
	require.NoError(t, err)
	assert.Contains(t, fields["DatabaseConnection.Password"], "Secret")
	assert.Contains(t, fields["DatabaseConnection.Password"], "ConfigMap")
	assert.NotEmpty(t, typeDocs["Config"])
}

func TestDescribe(t *testing.T) {
	assert.Equal(t, "cache kind --cache-type, CACHE", describe(fieldNode{
		owner: "Server", goName: "CacheType", leaf: true, flagName: "cache-type", envName: "CACHE",
	}, map[string]string{"Server.CacheType": "cache kind"}, nil))

	assert.Equal(t, "--kubeconfig", describe(fieldNode{
		leaf: true, flagName: "kubeconfig",
	}, nil, nil))

	assert.Equal(t, "settings", describe(fieldNode{
		value: reflect.ValueOf(types.Global{}), owner: "Config", goName: "Global",
	}, nil, map[string]string{"Global": "settings"}))
}

func TestSchemaDefault(t *testing.T) {
	on := true
	count := 3
	items := []string{"a", "b"}
	tests := []struct {
		name    string
		node    fieldNode
		want    string
		wantErr bool
	}{
		{name: "absent", node: fieldNode{value: reflect.ValueOf("")}},
		{name: "bool", node: fieldNode{value: reflect.ValueOf(on), hasDef: true, def: "true", path: "on"}, want: "true"},
		{name: "int", node: fieldNode{value: reflect.ValueOf(count), hasDef: true, def: "3", path: "count"}, want: "3"},
		{name: "list", node: fieldNode{value: reflect.ValueOf(items), hasDef: true, def: "a, b", path: "items"}, want: `["a","b"]`},
		{name: "text", node: fieldNode{value: reflect.ValueOf(types.Duration{}), hasDef: true, def: "5m", path: "wait"}, want: `"5m"`},
		{name: "bad bool", node: fieldNode{value: reflect.ValueOf(on), hasDef: true, def: "nope", path: "on"}, wantErr: true},
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
	schema, err := nodeSchema(fieldNode{
		value: reflect.ValueOf(types.Global{}),
		children: []fieldNode{{
			fileKey:  "name",
			owner:    "Sample",
			goName:   "Name",
			leaf:     true,
			flagName: "name",
			envName:  "NAME",
			hasDef:   true,
			def:      "porch",
			path:     "name",
			value:    reflect.ValueOf(""),
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
				"description": "the name --name, NAME",
				"default": "porch"
			}
		},
		"additionalProperties": false
	}`, string(raw))

	_, err = nodeSchema(fieldNode{leaf: true, path: "ratio", value: reflect.ValueOf(1.5)}, nil, nil)
	assert.Error(t, err)
}

func TestSchemaFieldsUsesComponentFlagNames(t *testing.T) {
	fields, err := schemaFields(&types.Config{})
	require.NoError(t, err)
	var flagName string
	for _, field := range fields {
		if field.goName != "Global" {
			continue
		}
		for _, child := range field.children {
			if child.goName == "DefaultImagePrefix" {
				flagName = child.flagName
			}
		}
	}
	assert.Equal(t, "default-image-prefix", flagName)

	var parent struct {
		Child struct {
			Name string
		}
	}
	fields, err = schemaFields(&parent)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	require.Len(t, fields[0].children, 1)
	assert.Equal(t, "child.name", fields[0].children[0].flagName)
}
