// Copyright 2024-2026 The kpt Authors
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
	"bufio"
	"bytes"
	stdcmp "cmp"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	updateGoldenFiles       = "UPDATE_GOLDEN_FILES"
	defaultTestGitServerUrl = "http://gitea.gitea.svc.cluster.local:3000"
	porchTestRepo           = "porch-test"
)

type CliTestSuite struct {
	// The path of the directory containing the test cases.
	TestDataPath string
	// SearchAndReplace contains (search, replace) pairs that will be applied to the command output before comparison.
	SearchAndReplace map[string]string
	// GitServerURL is the URL of the git server to use for the tests.
	GitServerURL string
	// PorchctlCommand is the full path to the porchctl command to be tested.
	PorchctlCommand string
	// DeleteNamespaceFunc is the function used to clean up test namespaces.
	// Defaults to KubectlDeleteNamespace. Override for v1alpha2 to handle CRD finalizers.
	DeleteNamespaceFunc func(t *testing.T, name string)
}

// NewCliTestSuite creates a new CliTestSuite based on the configuration in the testdata directory.
func NewCliTestSuite(t *testing.T, testdataDir string) *CliTestSuite {
	var err error

	s := &CliTestSuite{}
	// set base dir
	s.TestDataPath, err = filepath.Abs(testdataDir)
	if err != nil {
		t.Fatalf("Failed to get absolute path to testdata directory: %v", err)
	}
	// find porchctl to test
	s.PorchctlCommand, err = filepath.Abs(filepath.Join("..", "..", "..", ".build", "porchctl"))
	if err != nil {
		t.Fatalf("Failed to get absolute path to .build/porchctl command: %v", err)
	}
	if _, err := os.Stat(s.PorchctlCommand); err != nil {
		t.Fatalf("porchctl command not found at %q: %v", s.PorchctlCommand, err)
	}

	isPorchInCluster := IsPorchServerRunningInCluster(t)
	isControllerInCluster := IsRepoControllerRunningInCluster(t)
	// Use gitea cluster DNS only if BOTH are in-cluster
	if isPorchInCluster && isControllerInCluster {
		s.GitServerURL = defaultTestGitServerUrl
	} else {
		ip := KubectlWaitForLoadBalancerIp(t, "gitea", "gitea-lb")
		s.GitServerURL = "http://" + ip + ":3000"
	}
	s.SearchAndReplace = map[string]string{}
	if s.GitServerURL != defaultTestGitServerUrl {
		s.SearchAndReplace[defaultTestGitServerUrl] = s.GitServerURL
	}

	// prepare tmp directory used by the commands in the test cases
	err = runUtilityCommand(t, "rm", "-rf", "/tmp/porch-e2e")
	if err != nil {
		t.Fatalf("Failed to clean up older run: %v", err)
	}
	err = runUtilityCommand(t, "mkdir", "/tmp/porch-e2e")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Default namespace cleanup uses v1alpha1 finalizer removal
	s.DeleteNamespaceFunc = KubectlDeleteNamespace

	return s
}

// RunTests runs the test cases in the testdata directory.
func (s *CliTestSuite) RunTests(t *testing.T) {
	testCases := s.ScanTestCases(t)
	for _, tc := range testCases {
		t.Run(tc.TestCase, func(t *testing.T) {
			if tc.Skip != "" {
				t.Skipf("Skipping test: %s", tc.Skip)
			}
			s.RunTestCase(t, tc)
		})
	}
}

// RunTestCase runs a single test case.
func (s *CliTestSuite) RunTestCase(t *testing.T, tc TestCaseConfig) {
	KubectlCreateNamespace(t, tc.TestCase)

	// Setup signal handler for Ctrl-C cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		t.Logf("Interrupt received, cleaning up namespace %s", tc.TestCase)
		s.DeleteNamespaceFunc(t, tc.TestCase)
		if tc.UsesPorchTestRepo {
			suiteutils.RecreateGiteaRepo(t, porchTestRepo)
		}
		os.Exit(1)
	}()

	t.Cleanup(func() {
		signal.Stop(sigChan)
		s.DeleteNamespaceFunc(t, tc.TestCase)
		if tc.UsesPorchTestRepo {
			suiteutils.RecreateGiteaRepo(t, porchTestRepo)
		}
	})

	for i := range tc.Commands {
		// time.Sleep(1 * time.Second) // TODO: why was this necessary?
		command := &tc.Commands[i]

		// Build execution args without mutating the original command (preserves golden file content)
		execArgs := make([]string, len(command.Args))
		copy(execArgs, command.Args)
		for j := range execArgs {
			for search, replace := range s.SearchAndReplace {
				execArgs[j] = strings.ReplaceAll(execArgs[j], search, replace)
			}
		}
		if execArgs[0] == "porchctl" {
			// make sure that we are testing the porchctl command built from this codebase
			execArgs[0] = s.PorchctlCommand
		}
		cmd := exec.Command(execArgs[0], execArgs[1:]...)

		var stdout, stderr bytes.Buffer
		if command.Stdin != "" {
			cmd.Stdin = strings.NewReader(command.Stdin)
		}
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		t.Logf("running command %v", strings.Join(cmd.Args, " "))
		err := cmd.Run()

		if command.Yaml {
			reorderYamlStdout(t, &stdout)
		} else {
			reorderCommandStdout(t, &stdout)
		}

		cleanupStderr(t, &stderr)

		stdoutStr := stdout.String()
		stderrStr := stderr.String()
		for search, replace := range s.SearchAndReplace {
			command.Stdout = strings.ReplaceAll(command.Stdout, search, replace)
			command.Stderr = strings.ReplaceAll(command.Stderr, search, replace)
		}

		if command.StdErrTabToWhitespace {
			stderrStr = strings.ReplaceAll(stderrStr, "\t", "  ") // Replace tabs with spaces
		}

		if len(command.IgnoreColumns) > 0 {
			stdoutStr = stripColumns(stdoutStr, command.IgnoreColumns)
			command.Stdout = stripColumns(command.Stdout, command.IgnoreColumns)
		}

		if command.IgnoreWhitespace {
			command.Stdout = normalizeWhitespace(command.Stdout)
			command.Stderr = normalizeWhitespace(command.Stderr)
			stdoutStr = normalizeWhitespace(stdoutStr)
			stderrStr = normalizeWhitespace(stderrStr)
		}

		if strings.Contains(command.Stdout, placeholderAny) {
			command.Stdout = replacePlaceholders(command.Stdout, stdoutStr)
		}

		if os.Getenv(updateGoldenFiles) != "" {
			// NOTE: updateCommand overwrites Stdout/Stderr with raw output,
			// discarding {{ANY}} placeholders and ignoreColumns transformations.
			// After regenerating golden files, manually restore placeholders.
			updateCommand(command, err, stdout.String(), stderr.String())
		}

		if got, want := exitCode(err), command.ExitCode; got != want {
			t.Errorf("unexpected exit code from '%s'; got %d, want %d", strings.Join(command.Args, " "), got, want)
		}
		if got, want := stdoutStr, command.Stdout; got != want {
			t.Errorf("unexpected stdout content from '%s'; (-want, +got) %s", strings.Join(command.Args, " "), cmp.Diff(want, got))
		}
		got, want := stderrStr, command.Stderr
		got = removeArmPlatformWarning(got)

		if command.ContainsErrorString {
			if !strings.Contains(got, want) {
				t.Errorf("unexpected stderr content from '%s'; \n Error we got = \n(%s) \n Should contain substring = \n(%s)\n", strings.Join(command.Args, " "), got, want)
			}
		} else {
			if got != want {
				t.Errorf("unexpected stderr content from '%s'; (-want, +got) %s", strings.Join(command.Args, " "), cmp.Diff(want, got))
			}
		}

		if slices.Contains(cmd.Args, "register") {
			name, found := getRepoName(cmd.Args)
			if found {
				KubectlWaitForRepoReady(t, name, tc.TestCase)
			} else {
				t.Fatalf("Failed to get repo name for registration: %s", tc.TestCase)
			}
		}

		if command.WaitForReady && err == nil {
			prName := parsePRNameFromOutput(stdout.String())
			if prName != "" {
				KubectlWaitForPackageRevisionReady(t, prName, tc.TestCase)
			}
		}

		if command.WaitForPublished && err == nil {
			prName := parsePRNameFromOutput(stdout.String())
			if prName != "" {
				KubectlWaitForPackageRevisionPublished(t, prName, tc.TestCase)
			}
		}

		if command.WaitForRendered && err == nil {
			prName := parsePRNameFromOutput(stdout.String())
			if prName != "" {
				KubectlWaitForPackageRevisionRendered(t, prName, tc.TestCase)
			}
		}
	}

	if os.Getenv(updateGoldenFiles) != "" {
		WriteTestCaseConfig(t, &tc)
	}
}

// ScanTestCases parses the test case configs from the testdata directory.
func (s *CliTestSuite) ScanTestCases(t *testing.T) []TestCaseConfig {
	testCases := []TestCaseConfig{}

	if err := filepath.WalkDir(s.TestDataPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == s.TestDataPath {
			return nil
		}

		tc := ReadTestCaseConfig(t, d.Name(), path)
		testCases = append(testCases, tc)

		return nil
	}); err != nil {
		t.Fatalf("Failed to scan test cases: %v", err)
	}

	return testCases
}

func runUtilityCommand(t *testing.T, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	t.Logf("running utility command %s %s", command, strings.Join(args, " "))
	return cmd.Run()
}

func normalizeWhitespace(s1 string) string {
	parts := strings.Split(s1, " ")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			words = append(words, part)
		}
	}
	return strings.Join(words, " ")
}

// placeholderAny matches any non-empty value in a cell position during output comparison.
const placeholderAny = "{{ANY}}"

// stripColumns removes named columns from table-formatted output.
// It parses the header row to find column byte offsets, then removes
// those byte ranges from all rows. Returns the input unchanged if
// the header doesn't contain any of the named columns.
func stripColumns(output string, columns []string) string {
	if len(columns) == 0 || output == "" {
		return output
	}

	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return output
	}

	ranges := findColumnRanges(lines[0], columns)
	if len(ranges) == 0 {
		return output
	}

	var result []string
	for _, line := range lines {
		stripped := removeRanges(line, ranges)
		stripped = strings.TrimRight(stripped, " ")
		result = append(result, stripped)
	}
	return strings.Join(result, "\n")
}

// colRange represents a byte range [start, end) within a line.
type colRange struct{ start, end int }

// findColumnRanges locates the byte ranges for the given column names in the header.
// Returns ranges sorted right-to-left for safe removal.
// For the last column in a row (no subsequent column), the range extends to end-of-line
// so data values wider than the header label are fully removed.
// Column names are matched as whole words (bounded by start-of-string, end-of-string, or spaces).
func findColumnRanges(header string, columns []string) []colRange {
	var ranges []colRange
	for _, col := range columns {
		idx := findWholeColumn(header, col)
		if idx == -1 {
			continue
		}
		end := idx + len(col)
		// Consume trailing spaces to reach the next column header.
		for end < len(header) && header[end] == ' ' {
			end++
		}
		// If we reached end-of-header, this is the last column — mark end as -1
		// so removeRanges extends to end-of-line for each row.
		if end >= len(header) {
			ranges = append(ranges, colRange{idx, -1})
		} else {
			ranges = append(ranges, colRange{idx, end})
		}
	}
	slices.SortFunc(ranges, func(a, b colRange) int {
		return stdcmp.Compare(b.start, a.start)
	})
	return ranges
}

// findWholeColumn finds a column name in the header that is bounded by
// start-of-string/spaces on the left and end-of-string/spaces on the right.
// Returns -1 if not found as a whole word.
func findWholeColumn(header, col string) int {
	start := 0
	for {
		idx := strings.Index(header[start:], col)
		if idx == -1 {
			return -1
		}
		idx += start
		leftOK := idx == 0 || header[idx-1] == ' '
		rightEnd := idx + len(col)
		rightOK := rightEnd >= len(header) || header[rightEnd] == ' '
		if leftOK && rightOK {
			return idx
		}
		start = idx + 1
	}
}

// removeRanges strips the given byte ranges from a line (ranges must be sorted right-to-left).
// A range with end == -1 means "to end of line" (last column).
func removeRanges(line string, ranges []colRange) string {
	for _, r := range ranges {
		if r.start >= len(line) {
			continue
		}
		end := r.end
		if end == -1 || end > len(line) {
			end = len(line)
		}
		line = line[:r.start] + line[end:]
	}
	return line
}

// replacePlaceholders replaces {{ANY}} tokens in the expected string with the
// corresponding whitespace-delimited value from the actual string. This allows
// non-deterministic values (timestamps, ages, resource versions) to be ignored
// in comparisons.
//
// NOTE: This function uses strings.Fields internally, which normalizes whitespace.
// It should be used with ignoreWhitespace: true to ensure consistent behavior.
func replacePlaceholders(expected, actual string) string {
	if !strings.Contains(expected, placeholderAny) {
		return expected
	}

	expectedLines := strings.Split(expected, "\n")
	actualLines := strings.Split(actual, "\n")

	var result []string
	for i, eLine := range expectedLines {
		if !strings.Contains(eLine, placeholderAny) || i >= len(actualLines) {
			result = append(result, eLine)
			continue
		}
		result = append(result, replacePlaceholdersInLine(eLine, actualLines[i]))
	}
	return strings.Join(result, "\n")
}

// replacePlaceholdersInLine replaces {{ANY}} tokens in a single line with
// the corresponding word from the actual line.
func replacePlaceholdersInLine(expectedLine, actualLine string) string {
	eWords := strings.Fields(expectedLine)
	aWords := strings.Fields(actualLine)

	var replaced []string
	aIdx := 0
	for _, ew := range eWords {
		if ew == placeholderAny && aIdx < len(aWords) {
			replaced = append(replaced, aWords[aIdx])
		} else {
			replaced = append(replaced, ew)
		}
		aIdx++
	}
	return strings.Join(replaced, " ")
}

func removeArmPlatformWarning(got string) string {
	got = strings.Replace(
		got,
		"    \"WARNING: The requested image's platform (linux/amd64) does not match the detected host platform (linux/arm64/v8) and no specific platform was requested\"\n",
		"",
		1)

	if strings.HasSuffix(got, "  Stderr:\n") {
		// The warning message was the only message on stderr
		return strings.Replace(got, "  Stderr:\n", "", 1)
	} else {
		// There are other messages on stderr, so leave the "Stderr:"" tag in place
		return got
	}
}

// remove PASS lines from kpt fn eval, which includes a duration and will vary
func cleanupStderr(t *testing.T, buf *bytes.Buffer) {
	scanner := bufio.NewScanner(buf)
	var newBuf bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "[PASS]") {
			newBuf.Write([]byte(line))
			newBuf.Write([]byte("\n"))
		}
	}

	buf.Reset()
	if _, err := buf.Write(newBuf.Bytes()); err != nil {
		t.Fatalf("Failed to update cleaned up stderr: %v", err)
	}
}

func reorderYamlStdout(t *testing.T, buf *bytes.Buffer) {
	if buf.Len() == 0 {
		return
	}

	// strip out the resourceVersion:, creationTimestamp:, uid:
	// because that will change with every run
	scanner := bufio.NewScanner(buf)
	var newBuf bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "resourceVersion:") &&
			!strings.Contains(line, "creationTimestamp:") &&
			!strings.Contains(line, "uid:") {
			newBuf.Write([]byte(line))
			newBuf.Write([]byte("\n"))
		}
	}

	var data any
	if err := yaml.Unmarshal(newBuf.Bytes(), &data); err != nil {
		// not yaml.
		return
	}

	var stable bytes.Buffer
	encoder := yaml.NewEncoder(&stable)
	encoder.SetIndent(2)
	if err := encoder.Encode(data); err != nil {
		t.Fatalf("Failed to re-encode yaml output: %v", err)
	}
	buf.Reset()
	if _, err := buf.Write(stable.Bytes()); err != nil {
		t.Fatalf("Failed to update reordered yaml output: %v", err)
	}
}
func reorderCommandStdout(t *testing.T, buf *bytes.Buffer) {
	if buf.Len() == 0 {
		return
	}

	scanner := bufio.NewScanner(buf)
	var newBuf bytes.Buffer
	var headerLine string
	var bodyLines []string

	if scanner.Scan() {
		headerLine = scanner.Text()
	} else {
		return
	}

	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	slices.Sort(bodyLines)

	newBuf.Write([]byte(headerLine))
	newBuf.Write([]byte("\n"))

	for i := range bodyLines {
		newBuf.Write([]byte(bodyLines[i]))
		newBuf.Write([]byte("\n"))
	}

	buf.Reset()
	if _, err := buf.Write(newBuf.Bytes()); err != nil {
		t.Fatalf("Failed to update reordered command output: %v", err)
	}
}

func updateCommand(command *Command, exit error, stdout, stderr string) {
	command.ExitCode = exitCode(exit)
	command.Stdout = stdout
	command.Stderr = stderr
}

func exitCode(exit error) int {
	var ee *exec.ExitError
	if errors.As(exit, &ee) {
		return ee.ExitCode()
	}
	return 0
}

func getRepoName(args []string) (string, bool) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--name=") {
			return strings.TrimPrefix(arg, "--name="), true
		}
	}
	return "", false
}

// parsePRNameFromOutput extracts a PackageRevision name from command output.
// It looks for lines like "git.basens-clone.clone-1 created" and returns the name part.
func parsePRNameFromOutput(output string) string {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		// Match patterns like "<name> created", "<name> updated", "<name> proposed"
		for _, suffix := range []string{" created", " updated", " proposed", " approved", " rejected", " pushed"} {
			if before, ok := strings.CutSuffix(line, suffix); ok {
				return before
			}
		}
	}
	return ""
}
