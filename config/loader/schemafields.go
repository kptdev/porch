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
	"reflect"

	"github.com/kptdev/porch/config/types"
)

// SchemaFields is fieldsOf, except each component of types.Config is walked
// as its own bind root so flag names are not prefixed with global. or server.
// It is used by schema to build porch.config.schema.json.
func SchemaFields(dst any) ([]FieldNode, error) {
	fields, err := fieldsOf(dst)
	if err != nil {
		return nil, err
	}
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.Elem().Type() != reflect.TypeFor[types.Config]() {
		return fields, nil
	}
	for i := range fields {
		if fields[i].Leaf {
			continue
		}
		children, err := fieldsOf(fields[i].Value.Addr().Interface())
		if err != nil {
			return nil, err
		}
		fields[i].Children = children
	}
	return fields, nil
}
