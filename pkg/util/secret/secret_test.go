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

package secret

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactDataSourcePassword(t *testing.T) {
	tests := map[string]string{
		"postgres://u:secret@h:5432/db?sslmode=disable":              "postgres://u:REDACTED@h:5432/db?sslmode=disable",
		"postgres://h/db?password=secret&sslmode=x":                  "postgres://h/db?password=REDACTED&sslmode=x",
		"postgres://h/db?PaSSwOrD=secret&sslmode=x":                  "postgres://h/db?PaSSwOrD=REDACTED&sslmode=x",
		"postgres://h/db?password=secret&password=secret2&sslmode=x": "postgres://h/db?password=REDACTED&sslmode=x",
		"host=h user=u password=secret dbname=db":                    "host=h user=u password=REDACTED dbname=db",
		"host=h password='se cret' dbname=db":                        "host=h password=REDACTED dbname=db",
		"u:secret@tcp(h:3306)/db":                                    "u:REDACTED@tcp(h:3306)/db",
		"host=h user=u dbname=db":                                    "host=h user=u dbname=db",
	}
	for in, want := range tests {
		got := in
		RedactDataSourcePassword(&got)
		assert.Equal(t, want, got)
		assert.NotContains(t, got, "secret")
		assert.NotContains(t, got, "se cret")
	}
}
