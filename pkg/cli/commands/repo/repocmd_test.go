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

package repo

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func executeCommand(root *cobra.Command, args ...string) (string, error) {
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs(args)

	err := root.Execute()

	return buf.String(), err
}

func TestRepoCommands(t *testing.T) {
	ctx := context.Background()
	version := "v1.0"

	commands := NewCommand(ctx, version)

	assert.NotNil(t, commands, "Expected non-nil commands")
	assert.Equal(t, "repo", commands.Use, "Expected 'Use' to be 'repo'")

	subcommands := commands.Commands()
	expectedSubcommands := []string{"reg", "get", "unreg", "sync"}

	for _, expected := range expectedSubcommands {
		found := false
		for _, sub := range subcommands {
			if sub.Name() == expected {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected subcommand '%s' to be present", expected)
	}

	flag := commands.PersistentFlags().Lookup("kubeconfig")
	assert.NotNil(t, flag, "Expected 'kubeconfig' flag to be present")

	err := commands.Execute()
	assert.Nil(t, err, "Expected no error on command execution")

	output, err := executeCommand(commands, "--help")
	assert.Nil(t, err, "Expected no error when running help command")
	assert.Contains(t, output, "repo", "Expected output to contain 'repo'")
}
