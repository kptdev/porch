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

package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Decimal units are powers of 1000.
const (
	KB DataSize = 1000
	MB          = KB * 1000
	GB          = MB * 1000
)

// Binary units are powers of 1024.
const (
	KiB DataSize = 1 << (10 * (iota + 1))
	MiB
	GiB
)

// units maps the unit names accepted by parse to their size in bytes.
// Names without an "i" are decimal. Names with an "i" are binary.
var units = map[string]DataSize{
	"":    1,
	"B":   1,
	"K":   KB,
	"KB":  KB,
	"KI":  KiB,
	"KIB": KiB,
	"M":   MB,
	"MB":  MB,
	"MI":  MiB,
	"MIB": MiB,
	"G":   GB,
	"GB":  GB,
	"GI":  GiB,
	"GIB": GiB,
}

// TODO: remove pkg/util/datasize once callers use this type.
// DataSize is a number of bytes that marshals as a string such as "6MB" or "1Gi".
type DataSize int64

// UnmarshalJSON implements json.Unmarshaler.
func (s *DataSize) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	return s.set(str)
}

// MarshalJSON implements json.Marshaler.
func (s DataSize) MarshalJSON() ([]byte, error) {
	return json.Marshal(format(s))
}

// UnmarshalYAML implements yaml.Unmarshaler (gopkg.in/yaml.v2 and v3).
func (s *DataSize) UnmarshalYAML(unmarshal func(any) error) error {
	var str string
	if err := unmarshal(&str); err != nil {
		return err
	}
	return s.set(str)
}

// MarshalYAML implements yaml.Marshaler.
func (s DataSize) MarshalYAML() (any, error) {
	return format(s), nil
}

func (s *DataSize) set(str string) error {
	parsed, err := parse(str)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// parse parses a data size such as "6MB", "1Gi" or "6291456" and returns it in
// bytes. A number without a unit is a number of bytes. K, KB, M, MB, G and GB
// are powers of 1000. Ki, KiB, Mi, MiB, Gi and GiB are powers of 1024. Units
// are not case-sensitive. Fractions and negative sizes are not accepted.
func parse(s string) (DataSize, error) {
	trimmed := strings.TrimSpace(s)
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, fmt.Errorf("invalid data size %q: must start with a number", s)
	}
	number, err := strconv.ParseInt(trimmed[:digits], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid data size %q: %w", s, err)
	}
	unitName := strings.TrimSpace(trimmed[digits:])
	unit, ok := units[strings.ToUpper(unitName)]
	if !ok {
		return 0, fmt.Errorf("invalid data size %q: unknown unit %q, must be one of B, K, KB, Ki, KiB, M, MB, Mi, MiB, G, GB, Gi, GiB", s, unitName)
	}
	if number > math.MaxInt64/int64(unit) {
		return 0, fmt.Errorf("invalid data size %q: value is too large", s)
	}
	return DataSize(number) * unit, nil
}

// format returns the size in the largest unit that represents it exactly, for
// example "6MiB" for 6*1024*1024 and "6MB" for 6*1000*1000. parse reads the
// result back as the same size. A negative size is not a valid data size, and
// it is returned as a plain number.
func format(size DataSize) string {
	switch {
	case size < 0:
		return strconv.FormatInt(int64(size), 10)
	case size == 0:
		return "0B"
	case size%GiB == 0:
		return fmt.Sprintf("%dGiB", size/GiB)
	case size%GB == 0:
		return fmt.Sprintf("%dGB", size/GB)
	case size%MiB == 0:
		return fmt.Sprintf("%dMiB", size/MiB)
	case size%MB == 0:
		return fmt.Sprintf("%dMB", size/MB)
	case size%KiB == 0:
		return fmt.Sprintf("%dKiB", size/KiB)
	case size%KB == 0:
		return fmt.Sprintf("%dKB", size/KB)
	default:
		return fmt.Sprintf("%dB", size)
	}
}
