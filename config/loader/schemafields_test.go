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
	"testing"

	"github.com/kptdev/porch/config/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaFieldsUsesComponentFlagNames(t *testing.T) {
	fields, err := SchemaFields(&types.Config{})
	require.NoError(t, err)
	var flagName string
	for _, field := range fields {
		if field.GoName != "Global" {
			continue
		}
		for _, child := range field.Children {
			if child.GoName == "DefaultImagePrefix" {
				flagName = child.FlagName
			}
		}
	}
	assert.Equal(t, "default-image-prefix", flagName)

	var parent struct {
		Child struct {
			Name string
		}
	}
	fields, err = SchemaFields(&parent)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	require.Len(t, fields[0].Children, 1)
	assert.Equal(t, "child.name", fields[0].Children[0].FlagName)
}
