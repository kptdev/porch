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

	"github.com/stretchr/testify/assert"
)

func TestSplitWords(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{name: "", want: nil},
		{name: "DB", want: []string{"DB"}},
		{name: "MaxGRPCMessageSize", want: []string{"Max", "GRPC", "Message", "Size"}},
		{name: "PodTTL", want: []string{"Pod", "TTL"}},
		{name: "URLValue", want: []string{"URL", "Value"}},
		{name: "GoGitRepoCacheSize", want: []string{"Go", "Git", "Repo", "Cache", "Size"}},
		{name: "CreateV1Alpha2Rpkg", want: []string{"Create", "V", "1", "Alpha", "2", "Rpkg"}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, splitWords(tt.name), tt.name)
	}
}

func TestCamelKebabAndEnvCase(t *testing.T) {
	assert.Empty(t, camelCase(nil))

	words := splitWords("MaxGRPCMessageSize")
	assert.Equal(t, "maxGRPCMessageSize", camelCase(words))
	assert.Equal(t, "max-grpc-message-size", kebabCase(words))
	assert.Equal(t, "MAX_GRPC_MESSAGE_SIZE", envCase(words))

	acronym := splitWords("PodTTL")
	assert.Equal(t, "podTTL", camelCase(acronym))

	versioned := splitWords("CreateV1Alpha2Rpkg")
	assert.Equal(t, "createV1Alpha2Rpkg", camelCase(versioned))
	assert.Equal(t, "create-v-1-alpha-2-rpkg", kebabCase(versioned))
	assert.Equal(t, "CREATE_V_1_ALPHA_2_RPKG", envCase(versioned))
}

func TestTagName(t *testing.T) {
	name, skip := tagName("")
	assert.Empty(t, name)
	assert.False(t, skip)

	name, skip = tagName("functions")
	assert.Equal(t, "functions", name)
	assert.False(t, skip)

	name, skip = tagName("name,omitempty")
	assert.Equal(t, "name", name)
	assert.False(t, skip)

	name, skip = tagName("-")
	assert.Empty(t, name)
	assert.True(t, skip)

	name, skip = tagName("-,omitempty")
	assert.Empty(t, name)
	assert.True(t, skip)
}
