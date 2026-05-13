// Copyright 2025 The kpt Authors
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

package commands

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGetCommands tests the GetCommands function to ensure it returns the expected set of commands.
func TestGetCommands(t *testing.T) {
	ctx := context.Background()
	name := "test-cli"
	version := "v1.0"

	commands := GetCommands(ctx, name, version)

	assert.NotNil(t, commands, "Expected non-nil commands")
	assert.Greater(t, len(commands), 0, "Expected at least one command")

	expectedCommands := map[string]bool{
		"repo": false,
		"rpkg": false,
	}

	for _, cmd := range commands {
		if _, exists := expectedCommands[cmd.Name()]; exists {
			expectedCommands[cmd.Name()] = true
		}
	}

	for cmd, found := range expectedCommands {
		assert.True(t, found, "Expected command '%s' to be present", cmd)
	}
}
