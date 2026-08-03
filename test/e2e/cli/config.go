// Copyright 2022 The kpt Authors
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

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type Command struct {
	// Args is a list of args for the kpt CLI.
	Args []string `yaml:"args,omitempty"`
	// StdIn contents will be passed as the command's standard input, if not empty.
	Stdin string `yaml:"stdin,omitempty"`
	// StdOut is the standard output expected from the command.
	Stdout string `yaml:"stdout,omitempty"`
	// StdErr is the standard error output expected from the command.
	Stderr string `yaml:"stderr,omitempty"`
	// ExitCode is the expected exit code frm the command.
	ExitCode int `yaml:"exitCode,omitempty"`
	// Yaml indicates that stdout is yaml and the test will reformat it for stable ordering
	Yaml bool `yaml:"yaml,omitempty"`
	// IgnoreWhitespace indicates that whitespace differences should be ignored in the output
	IgnoreWhitespace bool `yaml:"ignoreWhitespace,omitempty"`
	// IgnoreColumns lists column names to strip from both expected and actual table output
	// before comparison. Useful for non-deterministic columns like AGE.
	IgnoreColumns []string `yaml:"ignoreColumns,omitempty"`
	// StdErrTabToWhitespace replaces "\t" (tab) character with whitespace "  "
	StdErrTabToWhitespace bool `yaml:"stdErrTabToWhitespace,omitempty"`
	// ContainsErrorString changes Stderr check from exact string match to contains string match
	ContainsErrorString bool `yaml:"containsErrorString,omitempty"`
	// WaitForReady waits for the PackageRevision named in stdout to become Ready.
	// Use for v1alpha2 async operations (clone, init, copy, push, etc.).
	WaitForReady bool `yaml:"waitForReady,omitempty"`
	// WaitForPublished waits for the PackageRevision named in stdout to be Published
	// with a non-zero status.revision. Use after approve commands where downstream
	// consumers need the revision number to be set.
	WaitForPublished bool `yaml:"waitForPublished,omitempty"`
	// WaitForRendered waits for the PackageRevision named in stdout to have Rendered=True.
	// Use after push commands with pipelines to ensure async render completes before propose/approve.
	WaitForRendered bool `yaml:"waitForRendered,omitempty"`
}

type TestCaseConfig struct {
	// TestCase is the name of the test case.
	TestCase string `yaml:"-"`
	// ConfigFile stores the name of the config file from which the config was loaded.
	// Used when generating or updating golden files.
	ConfigFile string `yaml:"-"`
	// UsesPorchTestRepo indicates if this test modifies the porch-test repo and needs it recreated after
	UsesPorchTestRepo bool `yaml:"usesPorchTestRepo,omitempty"`
	// Commands is a list of porchctl commands to be executed by the test.
	Commands []Command `yaml:"commands,omitempty"`
	// Skip the test? If the value is not empty, it will be used as a message with which to skip the test.
	Skip string `yaml:"skip,omitempty"`
}

func ReadTestCaseConfig(t *testing.T, name, path string) TestCaseConfig {
	configPath := filepath.Join(path, "config.yaml")
	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read test config file %q: %v", configPath, err)
	}

	var tc TestCaseConfig
	if err := yaml.Unmarshal(b, &tc); err != nil {
		t.Fatalf("Failed to unmarshal test config %q: %v", configPath, err)
	}

	tc.TestCase = name
	tc.ConfigFile = configPath
	return tc
}

func WriteTestCaseConfig(t *testing.T, tc *TestCaseConfig) {
	var out bytes.Buffer
	e := yaml.NewEncoder(&out)
	e.SetIndent(2)
	if err := e.Encode(tc); err != nil {
		t.Fatalf("Failed to marshal test case config for %s: %v", tc.TestCase, err)
	}
	if err := os.WriteFile(tc.ConfigFile, out.Bytes(), 0644); err != nil {
		t.Errorf("Failed to save test case config for %s into %q: %v", tc.TestCase, tc.ConfigFile, err)
	}
}
