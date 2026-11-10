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

package types

// TODO: maybe move this to the api module?

import (
	"encoding/json"
	"time"
)

// Duration is a time.Duration that marshals as a string such as "5m".
// UnmarshalJSON follows metav1.Duration. UnmarshalYAML accepts the same
// strings from gopkg.in/yaml.v3, which decodes a bare time.Duration as a
// nanosecond count and does not call UnmarshalJSON.
type Duration struct {
	time.Duration
}

func (d *Duration) IsZero() bool {
	return d.Duration == 0
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	return d.set(str)
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalYAML implements yaml.Unmarshaler (gopkg.in/yaml.v2 and v3).
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var str string
	if err := unmarshal(&str); err != nil {
		return err
	}
	return d.set(str)
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	return d.set(string(text))
}

func (d *Duration) set(str string) error {
	parsed, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

type durConstraint interface {
	int | int32 | int64 | uint | uint32 | uint64 | time.Duration
}

func DurationFrom[T durConstraint](v T) Duration {
	return Duration{Duration: time.Duration(v)}
}
