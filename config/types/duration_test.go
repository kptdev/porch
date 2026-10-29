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
	"time"

	"sigs.k8s.io/yaml"
)

func TestDurationJSON(t *testing.T) {
	var got Duration
	if err := json.Unmarshal([]byte(`"5m"`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Duration != 5*time.Minute {
		t.Fatalf("duration = %s", got.Duration)
	}

	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `"5m0s"` {
		t.Fatalf("marshal = %s", out)
	}

	if err := json.Unmarshal([]byte(`5`), &got); err == nil {
		t.Fatal("expected a number of nanoseconds to be rejected")
	}
}

func TestDurationYAML(t *testing.T) {
	var got struct {
		Wait Duration
	}
	if err := yaml.Unmarshal([]byte("wait: 30s\n"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Wait.Duration != 30*time.Second {
		t.Fatalf("duration = %s", got.Wait.Duration)
	}

	out, err := yaml.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again struct {
		Wait Duration
	}
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if again.Wait.Duration != got.Wait.Duration {
		t.Fatalf("round trip = %s", again.Wait.Duration)
	}
}
