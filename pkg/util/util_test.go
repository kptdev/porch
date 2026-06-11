// Copyright 2022,2025 The kpt Authors
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

package util

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGetPRWorkspaceName(t *testing.T) {
	wsName := GetPRWorkspaceName("")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("hello")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("hello.there")
	assert.Equal(t, "there", wsName)

	wsName = GetPRWorkspaceName(".")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("v.")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("hello.v1.2.3")
	assert.Equal(t, "v1.2.3", wsName)

	wsName = GetPRWorkspaceName("hello.v1.2")
	assert.Equal(t, "v1.2", wsName)

	wsName = GetPRWorkspaceName("hello.v1")
	assert.Equal(t, "v1", wsName)

	wsName = GetPRWorkspaceName("hello.v1.v1")
	assert.Equal(t, "v1", wsName)

	wsName = GetPRWorkspaceName("hello.v1.2.3.v4.5.6")
	assert.Equal(t, "v4.5.6", wsName)

	wsName = GetPRWorkspaceName("hello.v1.2.3.end")
	assert.Equal(t, "end", wsName)

	wsName = GetPRWorkspaceName("repo.")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("repo.hello")
	assert.Equal(t, "hello", wsName)

	wsName = GetPRWorkspaceName("repo.hello.there")
	assert.Equal(t, "there", wsName)

	wsName = GetPRWorkspaceName(".")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("repo.v.")
	assert.Equal(t, "", wsName)

	wsName = GetPRWorkspaceName("repo.hello.v1.2.3")
	assert.Equal(t, "v1.2.3", wsName)

	wsName = GetPRWorkspaceName("repo.hello.v1.2")
	assert.Equal(t, "v1.2", wsName)

	wsName = GetPRWorkspaceName("repo.hello.v1")
	assert.Equal(t, "v1", wsName)

	wsName = GetPRWorkspaceName("repo.hello.v1.v1")
	assert.Equal(t, "v1", wsName)

	wsName = GetPRWorkspaceName("repo.hello.v1.2.3.v4.5.6")
	assert.Equal(t, "v4.5.6", wsName)

	wsName = GetPRWorkspaceName("repo.hello.v1.2.3.end")
	assert.Equal(t, "end", wsName)
}

func TestParseRepositoryNameOK(t *testing.T) {
	testCases := map[string]struct {
		pkgRevId string
		expected []string
	}{
		"no-delimiter": {
			pkgRevId: "my-repo",
			expected: []string{"my-repo", "", ""},
		},
		"one-delimiter": {
			pkgRevId: "my-repo.my-package-name",
			expected: []string{"my-repo", "", "my-package-name"},
		},
		"two-delimiters": {
			pkgRevId: "my-repo.my-package-name.my-workspace",
			expected: []string{"my-repo", "my-package-name", "my-workspace"},
		},
		"three-delimiters": {
			pkgRevId: "my-repo.my-root-dir.my-package-name.my-workspace",
			expected: []string{"my-repo", "my-root-dir.my-package-name", "my-workspace"},
		},
		"four-delimitesr": {
			pkgRevId: "my-repo.my-root-dir.my-sub-dir.my-package-name.my-workspace",
			expected: []string{"my-repo", "my-root-dir.my-sub-dir.my-package-name", "my-workspace"},
		},
		"five-delimiters": {
			pkgRevId: "my-repo.my-root-dir.my-sub-dir.my-sub-dir.my-package-name.my-workspace",
			expected: []string{"my-repo", "my-root-dir.my-sub-dir.my-sub-dir.my-package-name", "my-workspace"},
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			parsedSlice := SplitIn3OnDelimiter(tc.pkgRevId, ".")
			if !reflect.DeepEqual(tc.expected, parsedSlice) {
				t.Errorf("expected %+v, got %+v", tc.expected, parsedSlice)
			}
		})
	}
}

func TestParseRepositoryNameStrangeDelimiterOK(t *testing.T) {
	testCases := map[string]struct {
		pkgRevId string
		expected []string
	}{
		"no-delimiter": {
			pkgRevId: "my-repo",
			expected: []string{"my-repo", "", ""},
		},
		"one-delimiter": {
			pkgRevId: "my-repo#.#my-package-name",
			expected: []string{"my-repo", "", "my-package-name"},
		},
		"two-delimiters": {
			pkgRevId: "my-repo#.#my-package-name#.#my-workspace",
			expected: []string{"my-repo", "my-package-name", "my-workspace"},
		},
		"three-delimiters": {
			pkgRevId: "my-repo#.#my-root-dir#.#my-package-name#.#my-workspace",
			expected: []string{"my-repo", "my-root-dir#.#my-package-name", "my-workspace"},
		},
		"four-delimitesr": {
			pkgRevId: "my-repo#.#my-root-dir#.#my-sub-dir#.#my-package-name#.#my-workspace",
			expected: []string{"my-repo", "my-root-dir#.#my-sub-dir#.#my-package-name", "my-workspace"},
		},
		"five-delimiters": {
			pkgRevId: "my-repo#.#my-root-dir#.#my-sub-dir#.#my-sub-dir#.#my-package-name#.#my-workspace",
			expected: []string{"my-repo", "my-root-dir#.#my-sub-dir#.#my-sub-dir#.#my-package-name", "my-workspace"},
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			parsedSlice := SplitIn3OnDelimiter(tc.pkgRevId, "#.#")
			if !reflect.DeepEqual(tc.expected, parsedSlice) {
				t.Errorf("expected %+v, got %+v", tc.expected, parsedSlice)
			}
		})
	}
}

func TestParsePkgRevNameErr(t *testing.T) {
	testCases := map[string]struct {
		pkgRevId string
	}{
		"no-dot": {
			pkgRevId: "my-repomy-package-namemy-workspace",
		},
		"one-dot-one": {
			pkgRevId: "my-repomy-package-name.my-workspace",
		},
		"one-dot-two": {
			pkgRevId: "my-repo.my-package-namemy-workspace",
		},
		"rev-3-dash": {
			pkgRevId: "my-package-name-akoshjadfhijao[ifj[adsfj[adsf",
		},
		"rev-1-dash": {
			pkgRevId: "mypackagename-akoshjadfhijao[ifj[adsfj[adsf",
		},
		"rev-1-dash-no-suffix": {
			pkgRevId: "mypackagename-",
		},
		"no-dash": {
			pkgRevId: "mypackagenameakoshjadfhijao[ifj[adsfj[adsf",
		},
		"empty": {
			pkgRevId: "",
		},
		"white-space": {
			pkgRevId: "   \t \n  ",
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			splitPRName := SplitIn3OnDelimiter(tc.pkgRevId, ".")
			if splitPRName[1] != "" {
				t.Errorf("expected a blank package name on pr name %q but got %q", tc.pkgRevId, splitPRName[1])
			}
		})
	}
}

func TestValidatePkgRevName(t *testing.T) {
	testCases := map[string]struct {
		Repo          string
		Path          string
		Pkg           string
		Ws            string
		Err           bool
		PrErrString   string
		RepoErrString string
		PathErrString string
		PkgErrString  string
		WsErrString   string
	}{}

	testFileName := filepath.Join("testdata", "pkg-rev-name.yaml")

	testCasesBA, err := os.ReadFile(testFileName)
	if err != nil {
		t.Errorf("could not read test case data from %s: %v", testFileName, err)
		return
	}

	err = yaml.Unmarshal(testCasesBA, &testCases)
	if err != nil {
		t.Errorf("could not unmarshal test case data from %s: %v", testFileName, err)
		return
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			got := ValidPkgRevObjName(tc.Repo, tc.Path, tc.Pkg, tc.Ws)
			if got == nil {
				if tc.Err == true {
					t.Errorf("didn't get an an error when expecting one")
				}
				return
			}

			errorSlice := strings.Split(got.Error(), "\n")

			assert.Equal(t, errorSlice[0], "package revision kubernetes resource name invalid:")

			assertExpectedPartErrExists(t, errorSlice,
				tc.PrErrString,
				"package revision kubernetes name ",
				ComposePkgRevObjName(tc.Repo, tc.Path, tc.Pkg, tc.Ws))

			assertExpectedPartErrExists(t, errorSlice,
				tc.RepoErrString,
				"repository name ",
				tc.Repo)

			assertExpectedPartErrExists(t, errorSlice,
				tc.PathErrString,
				"path ",
				tc.Path)

			assertExpectedPartErrExists(t, errorSlice,
				tc.PkgErrString,
				"package name ",
				tc.Pkg)

			assertExpectedPartErrExists(t, errorSlice,
				tc.WsErrString,
				"workspace name ",
				tc.Ws)
		})
	}
}

func TestValidatePkgName(t *testing.T) {
	testCases := map[string]struct {
		Repo          string
		Path          string
		Pkg           string
		Ws            string
		Err           bool
		PrErrString   string
		RepoErrString string
		PathErrString string
		PkgErrString  string
		WsErrString   string
	}{}

	testFileName := filepath.Join("testdata", "pkg-name.yaml")

	testCasesBA, err := os.ReadFile(testFileName)
	if err != nil {
		t.Errorf("could not read test case data from %s: %v", testFileName, err)
		return
	}

	err = yaml.Unmarshal(testCasesBA, &testCases)
	if err != nil {
		t.Errorf("could not unmarshal test case data from %s: %v", testFileName, err)
		return
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			got := ValidPkgObjName(tc.Repo, tc.Path, tc.Pkg)
			if got == nil {
				if tc.Err == true {
					t.Errorf("didn't get an an error when expecting one")
				}
				return
			}

			errorSlice := strings.Split(got.Error(), "\n")

			assert.Equal(t, errorSlice[0], "package kubernetes resource name invalid:")

			assertExpectedPartErrExists(t, errorSlice,
				tc.PrErrString,
				"package kubernetes resource name invalid: ",
				ComposePkgObjName(tc.Repo, tc.Path, tc.Pkg))

			assertExpectedPartErrExists(t, errorSlice,
				tc.RepoErrString,
				"repository name ",
				tc.Repo)

			assertExpectedPartErrExists(t, errorSlice,
				tc.PathErrString,
				"path ",
				tc.Path)

			assertExpectedPartErrExists(t, errorSlice,
				tc.PkgErrString,
				"package name ",
				tc.Pkg)
		})
	}
}

func TestGenerateUid(t *testing.T) {
	assert.Equal(t, types.UID("272983f7-a30d-58e0-a809-c38e8638666d"), GenerateUid("my-prefix", "my-namespace", "my.kube.name.of-something"))
}

func assertExpectedPartErrExists(t *testing.T, errorSlice []string, errString, prefix, part string) {
	if len(errString) == 0 {
		return
	}

	errStart := prefix + part + " invalid:"

	partErrMsg := getPartErrMsg(errorSlice, errStart)
	if len(partErrMsg) == 0 {
		t.Errorf("expected to find error message starting with: %q", errStart)
	} else {
		if result := strings.Contains(partErrMsg, errString); result != true {
			t.Errorf("error message %q should contain %q", partErrMsg, errString)
		}
	}
}

func TestCompareObjectMeta(t *testing.T) {
	left := metav1.ObjectMeta{}
	right := metav1.ObjectMeta{}
	assert.True(t, CompareObjectMeta(left, right))

	left.Name = "my_name"
	assert.False(t, CompareObjectMeta(left, right))
	right.Name = "my_name"
	assert.True(t, CompareObjectMeta(left, right))

	left.Namespace = "my_ns"
	assert.False(t, CompareObjectMeta(left, right))
	right.Namespace = "my_ns"
	assert.True(t, CompareObjectMeta(left, right))

	left.Labels = map[string]string{}
	assert.False(t, CompareObjectMeta(left, right))
	right.Labels = map[string]string{}
	assert.True(t, CompareObjectMeta(left, right))

	left.Annotations = map[string]string{}
	assert.False(t, CompareObjectMeta(left, right))
	right.Annotations = map[string]string{}
	assert.True(t, CompareObjectMeta(left, right))

	left.Finalizers = []string{}
	assert.False(t, CompareObjectMeta(left, right))
	right.Finalizers = []string{}
	assert.True(t, CompareObjectMeta(left, right))

	left.OwnerReferences = []metav1.OwnerReference{}
	assert.False(t, CompareObjectMeta(left, right))
	right.OwnerReferences = []metav1.OwnerReference{}
	assert.True(t, CompareObjectMeta(left, right))
}

func TestRetryOnErrorConditional(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		shouldRetryFunc func(error) bool
		retryCount      int
		shouldFail      bool
	}{
		{
			name:            "no error",
			err:             nil,
			shouldRetryFunc: func(error) bool { return false },
			retryCount:      2,
			shouldFail:      false,
		},
		{
			name:            "error without retries",
			err:             fmt.Errorf("some error"),
			shouldRetryFunc: func(error) bool { return false },
			retryCount:      2,
			shouldFail:      true,
		},
		{
			name:            "error with retries",
			err:             fmt.Errorf("some error"),
			shouldRetryFunc: func(error) bool { return true },
			retryCount:      10,
			shouldFail:      true,
		},
		{
			name:            "no tries",
			err:             fmt.Errorf("some error"),
			shouldRetryFunc: func(error) bool { return true },
			retryCount:      0,
			shouldFail:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RetryOnErrorConditional(tt.retryCount, tt.shouldRetryFunc, func(_ int) error {
				return tt.err
			})

			if (got != nil) != tt.shouldFail {
				t.Errorf("RetryOnErrorConditional() = %v, shouldFail %v", got, tt.shouldFail)
			}
		})
	}
}

func getPartErrMsg(errorSlice []string, start string) string {
	for i := range errorSlice {
		if strings.HasPrefix(errorSlice[i], start) {
			return errorSlice[i]
		}
	}

	return ""
}
