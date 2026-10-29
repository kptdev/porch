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

import (
	"encoding/json"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestDataSizeJSON(t *testing.T) {
	var got DataSize
	if err := json.Unmarshal([]byte(`"6MB"`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != 6*MB {
		t.Fatalf("size = %d", got)
	}

	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `"6MB"` {
		t.Fatalf("marshal = %s", out)
	}

	if err := json.Unmarshal([]byte(`6291456`), &got); err == nil {
		t.Fatal("expected a bare number to be rejected")
	}
}

func TestDataSizeUnits(t *testing.T) {
	cases := map[string]DataSize{
		`"1Gi"`:   GiB,
		`"2GiB"`:  2 * GiB,
		`"512Ki"`: 512 * KiB,
		`"4Mi"`:   4 * MiB,
		`"8K"`:    8 * KB,
	}
	for input, want := range cases {
		var got DataSize
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", input, err)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", input, got, want)
		}
	}
}

func TestDataSizeYAML(t *testing.T) {
	var got struct {
		Limit DataSize
	}
	if err := yaml.Unmarshal([]byte("limit: 1Gi\n"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Limit != GiB {
		t.Fatalf("size = %d", got.Limit)
	}

	out, err := yaml.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again struct {
		Limit DataSize
	}
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if again.Limit != got.Limit {
		t.Fatalf("round trip = %d", again.Limit)
	}
}
