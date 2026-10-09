// Copyright 2022, 2026 The kpt Authors
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

package fuzzer

import (
	fnresult "github.com/kptdev/kpt/api/fnresult/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/randfill"
)

var Funcs = func(codecs runtimeserializer.CodecFactory) []any {
	return []any{
		func(e *fnresult.Field, c randfill.Continue) {
			c.FillNoCustom(e)
			// Field.UnmarshalJSON trims path, currentValue, and proposedValue.
			switch c.Bool() {
			case true:
				e.Path = ""
			case false:
				e.Path = ".spec.containers[0].resources"
			}

			switch c.Bool() {
			case true:
				e.CurrentValue = ""
			case false:
				e.CurrentValue = `requests:
  memory: 512Mi
  cpu: 1000m`
			}
			switch c.Bool() {
			case true:
				e.ProposedValue = ""
			case false:
				e.ProposedValue = `requests:
  memory: 1Gi
  cpu: 1`
			}
		},
	}
}
