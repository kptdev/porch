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
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/kptdev/porch/config/types"
	"github.com/kptdev/porch/config/types/datasize"
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
	return walk(fields, func(node *fieldNode) error {
		if !node.hasDef {
			return nil
		}
		if err := assignString(node.value, node.def); err != nil {
			return fmt.Errorf("config field %s: cannot parse default %q: %w", node.path, node.def, err)
		}
		return nil
	})
}

func registerFlags(flags *pflag.FlagSet, fields []fieldNode) error {
	return walk(fields, func(node *fieldNode) error {
		if node.flagName == "" {
			return nil
		}
		if flags.Lookup(node.flagName) != nil {
			return fmt.Errorf("config field %s: duplicate flag %q", node.path, node.flagName)
		}
		return registerFlag(flags, node)
	})
}

func registerFlag(flags *pflag.FlagSet, node *fieldNode) error {
	switch node.value.Type() {
	case reflect.TypeFor[string]():
		flags.StringVar(node.value.Addr().Interface().(*string), node.flagName, node.value.String(), node.usage)
	case reflect.TypeFor[bool]():
		flags.Var(&boolValue{value: node.value}, node.flagName, node.usage)
		flags.Lookup(node.flagName).NoOptDefVal = "true"
	case reflect.TypeFor[int]():
		flags.IntVar(node.value.Addr().Interface().(*int), node.flagName, int(node.value.Int()), node.usage)
	case reflect.TypeFor[int64]():
		flags.Int64Var(node.value.Addr().Interface().(*int64), node.flagName, node.value.Int(), node.usage)
	case reflect.TypeFor[datasize.DataSize](), reflect.TypeFor[types.Duration](), reflect.TypeFor[[]string]():
		flags.Var(&scalarValue{value: node.value}, node.flagName, node.usage)
	default:
		return fmt.Errorf("config field %s: unsupported type %s", node.path, node.value.Type())
	}
	return nil
}

// scalarValue sets a Duration, DataSize, or string slice from a flag string.
// A repeated string-slice flag appends. The first occurrence replaces the default.
type scalarValue struct {
	value   reflect.Value
	changed bool
}

func (v *scalarValue) Set(raw string) error {
	if v.changed && v.value.Type() == reflect.TypeFor[[]string]() {
		parts := splitList(raw)
		current := v.value.Interface().([]string)
		next := make([]string, len(current), len(current)+len(parts))
		copy(next, current)
		v.value.Set(reflect.ValueOf(append(next, parts...)))
		return nil
	}
	if err := assignString(v.value, raw); err != nil {
		return err
	}
	v.changed = true
	return nil
}

func (v *scalarValue) String() string {
	switch val := v.value.Interface().(type) {
	case []string:
		return strings.Join(val, ",")
	case fmt.Stringer:
		return val.String()
	default:
		return ""
	}
}

func (v *scalarValue) Type() string {
	return "string"
}

// boolValue accepts the same spellings as parseBool, including yes and no.
// IsBoolFlag lets --name mean true.
type boolValue struct {
	value reflect.Value
}

func (b *boolValue) Set(raw string) error {
	parsed, err := parseBool(raw)
	if err != nil {
		return err
	}
	b.value.SetBool(parsed)
	return nil
}

func (b *boolValue) String() string { return strconv.FormatBool(b.value.Bool()) }

func (b *boolValue) Type() string { return "bool" }

func (b *boolValue) IsBoolFlag() bool { return true }
