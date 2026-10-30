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
	"strings"

	"github.com/kptdev/porch/config/types"
	"github.com/spf13/pflag"
)

// Bind applies default tags and registers dst's flags on fs.
// dst must be a non-nil pointer to a struct. Parse fs before calling Overlay.
func Bind(fs *pflag.FlagSet, dst any) error {
	if fs == nil {
		return fmt.Errorf("config flag set is nil")
	}
	fields, err := fieldsOf(dst)
	if err != nil {
		return err
	}
	if err := applyDefaults(fields); err != nil {
		return err
	}
	return registerFlags(fs, fields)
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
