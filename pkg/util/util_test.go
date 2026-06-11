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

	"github.com/kptdev/krm-functions-sdk/go/fn/kptfileapi"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestFindBestSemverMatch(t *testing.T) {
	cacheKeys := []string{
		"ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1",
		"ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4",
		"ghcr.io/kptdev/krm-functions-catalog/set-namespace@sha256:abcdef123456",
		"ghcr.io/kptdev/krm-functions-catalog/apply-replacements:v0.1.1",
		"ghcr.io/kptdev/krm-functions-catalog/apply-replacements:v0.1",
		"ghcr.io/kptdev/krm-functions-catalog/starlark:v0.4.3",
		"set-namespace:v0.4.1",
	}

	t.Run("selects highest matching version", func(t *testing.T) {
		cacheKeys := []string{
			"v0.4.1",
			"v0.4",
			"@sha256:abcdef123456",
		}
		key, err := FindBestSemverMatch(
			">= 0.4.0 < 0.5.0",
			"ghcr.io/kptdev/krm-functions-catalog/set-namespace",
			cacheKeys,
		)
		assert.NoError(t, err)
		assert.Equal(t, "v0.4.1", key)
	})

	t.Run("exact version match", func(t *testing.T) {
		cacheKeys := []string{
			"v0.1.1",
			"v0.1",
		}
		key, err := FindBestSemverMatch(
			"0.1.1",
			"ghcr.io/kptdev/krm-functions-catalog/apply-replacements",
			cacheKeys,
		)
		assert.NoError(t, err)
		assert.Equal(t, "v0.1.1", key)
	})

	t.Run("no matching version for valid constraint", func(t *testing.T) {
		_, err := FindBestSemverMatch(
			"> 1.0.0",
			"ghcr.io/kptdev/krm-functions-catalog/set-namespace",
			cacheKeys,
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no image matching")
	})

	t.Run("image not in cache", func(t *testing.T) {
		_, err := FindBestSemverMatch(
			">= 0.1.0",
			"ghcr.io/kptdev/krm-functions-catalog/nonexistent",
			cacheKeys,
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no image matching")
	})

	t.Run("invalid semver constraint", func(t *testing.T) {
		_, err := FindBestSemverMatch(
			">> 1.0.0",
			"ghcr.io/kptdev/krm-functions-catalog/set-namespace",
			cacheKeys,
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid semver constraint")
	})

	t.Run("skips sha256-tagged entries", func(t *testing.T) {
		cacheKeys := []string{
			"v0.4.1",
			"v0.4",
			"@sha256:abcdef123456",
		}
		key, err := FindBestSemverMatch(
			">= 0.4.0",
			"ghcr.io/kptdev/krm-functions-catalog/set-namespace",
			cacheKeys,
		)
		assert.NoError(t, err)
		assert.NotContains(t, key, "@sha256:")
	})

	t.Run("matches without registry prefix", func(t *testing.T) {
		cacheKeys := []string{
			"v0.4.1",
			"v0.4",
			"@sha256:abcdef123456",
		}
		key, err := FindBestSemverMatch(
			">= 0.4.0",
			"set-namespace",
			cacheKeys,
		)
		assert.NoError(t, err)
		assert.Equal(t, "v0.4.1", key)
	})

	t.Run("empty cache keys", func(t *testing.T) {
		_, err := FindBestSemverMatch(
			">= 0.1.0",
			"ghcr.io/kptdev/krm-functions-catalog/set-namespace",
			[]string{},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no image matching")
	})

	t.Run("selects greatest from multiple matches", func(t *testing.T) {
		keys := []string{
			"v1.0.0",
			"v1.1.0",
			"v1.2.0",
			"v2.0.0",
		}
		key, err := FindBestSemverMatch(">= 1.0.0 < 2.0.0", "myimage", keys)
		assert.NoError(t, err)
		assert.Equal(t, "v1.2.0", key)
	})

	t.Run("skips entries with unparseable versions", func(t *testing.T) {
		keys := []string{
			"v1.0.0",
			"vnotaversion",
		}
		key, err := FindBestSemverMatch(">= 1.0.0", "myimage", keys)
		assert.NoError(t, err)
		assert.Equal(t, "v1.0.0", key)
	})
}

func TestGetRepoPackageRefFromUpstream(t *testing.T) {
	tests := []struct {
		name           string
		upstream       *kptfileapi.Upstream
		wantRepoSpec   *configapi.RepositorySpec
		wantPkg        string
		wantRef        string
		wantManagedRef bool
		wantErr        string
	}{
		{
			name:     "nil upstream returns error",
			upstream: nil,
			wantErr:  "upstream does not contain a valid git repository",
		},
		{
			name:     "nil git returns error",
			upstream: &kptfileapi.Upstream{Git: nil},
			wantErr:  "upstream does not contain a valid git repository",
		},
		{
			name: "empty repo returns error",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "",
					Ref:       "mypkg/v1",
					Directory: "mypkg",
				},
			},
			wantErr: "upstream does not contain a valid git repository",
		},
		{
			name: "empty directory returns error",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "mypkg/v1",
					Directory: "",
				},
			},
			wantErr: "git directory reference",
		},
		{
			name: "directory with leading slash returns error",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "mypkg/v1",
					Directory: "/mypkg",
				},
			},
			wantErr: "git directory reference \"/mypkg\" in upstream is invalid",
		},
		{
			name: "directory with trailing slash returns error",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "mypkg/v1",
					Directory: "mypkg/",
				},
			},
			wantErr: "git directory reference \"mypkg/\" in upstream is invalid",
		},
		{
			name: "empty ref returns error",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "",
					Directory: "mypkg",
				},
			},
			wantErr: "git ref reference",
		},
		{
			name: "unmanaged ref - ref does not contain directory pattern",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "main",
					Directory: "mypkg",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/example/repo",
					Directory: "mypkg",
				},
			},
			wantPkg:        "mypkg",
			wantRef:        "main",
			wantManagedRef: false,
		},
		{
			name: "unmanaged ref - arbitrary tag",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "release-2.0",
					Directory: "packages/foo",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/example/repo",
					Directory: "packages/foo",
				},
			},
			wantPkg:        "packages.foo",
			wantRef:        "release-2.0",
			wantManagedRef: false,
		},
		{
			name: "managed ref - simple package",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "mypkg/v1",
					Directory: "mypkg",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/example/repo",
					Directory: "",
				},
			},
			wantPkg:        "mypkg",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "managed ref - nested package",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "packages/mypkg/v2",
					Directory: "packages/mypkg",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/example/repo",
					Directory: "",
				},
			},
			wantPkg:        "packages.mypkg",
			wantRef:        "v2",
			wantManagedRef: true,
		},
		{
			name: "managed ref - with repo directory prefix",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/example/repo.git",
					Ref:       "blueprints/mypkg/v3",
					Directory: "mypkg",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/example/repo",
					Directory: "blueprints",
				},
			},
			wantPkg:        "mypkg",
			wantRef:        "v3",
			wantManagedRef: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoSpec, pkg, ref, managedRef, err := GetRepoPackageRefFromUpstream(tt.upstream)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, repoSpec)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantRepoSpec, repoSpec)
				assert.Equal(t, tt.wantPkg, pkg)
				assert.Equal(t, tt.wantRef, ref)
				assert.Equal(t, tt.wantManagedRef, managedRef)
			}
		})
	}
}

func TestGetRepoPackageRefFromUpstreamRealData(t *testing.T) {
	tests := []struct {
		name           string
		upstream       *kptfileapi.Upstream
		wantRepoSpec   *configapi.RepositorySpec
		wantPkg        string
		wantRef        string
		wantManagedRef bool
		wantErr        string
	}{
		{
			name: "real test case 1",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "upstream-function1",
					Ref:       "upstream-function1/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "",
				},
			},
			wantPkg:        "upstream-function1",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 2",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "r2/r2/r2/r2",
					Ref:       "r2/r2/r2/r2/r2/r2/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "r2/r2",
				},
			},
			wantPkg:        "r2.r2.r2.r2",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 3",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "upstream-function",
					Ref:       "l1/upstream-function/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "l1",
				},
			},
			wantPkg:        "upstream-function",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 4",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "upstream-function",
					Ref:       "r1/upstream-function/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "r1",
				},
			},
			wantPkg:        "upstream-function",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 5",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "upstream-function",
					Ref:       "l3/l3/l3/upstream-function/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "l3/l3/l3",
				},
			},
			wantPkg:        "upstream-function",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 6",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "upstream-dir/upstream-function",
					Ref:       "l3/l3/l3/upstream-dir/upstream-function/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "l3/l3/l3",
				},
			},
			wantPkg:        "upstream-dir.upstream-function",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 7",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "upstream-function",
					Ref:       "r3/r3/r3/upstream-function/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "r3/r3/r3",
				},
			},
			wantPkg:        "upstream-function",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 8",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/kptdev/kpt.git",
					Directory: "package-examples/wordpress",
					Ref:       "v1.0.0-beta.64",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/kptdev/kpt",
					Directory: "package-examples/wordpress",
				},
			},
			wantPkg:        "package-examples.wordpress",
			wantRef:        "v1.0.0-beta.64",
			wantManagedRef: false,
		},
		{
			name: "real test case 9",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/kptdev/kpt-samples.git",
					Directory: "echo",
					Ref:       "main",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/kptdev/kpt-samples",
					Directory: "echo",
				},
			},
			wantPkg:        "echo",
			wantRef:        "main",
			wantManagedRef: false,
		},
		{
			name: "real test case 10",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/kptdev/kpt-samples.git",
					Directory: "echo",
					Ref:       "v0",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/kptdev/kpt-samples",
					Directory: "echo",
				},
			},
			wantPkg:        "echo",
			wantRef:        "v0",
			wantManagedRef: false,
		},
		{
			name: "real test case 11",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "http://172.18.255.204:3000/porch/porch-test.git",
					Directory: "r2/r2/r2/r2/r2/r2",
					Ref:       "r2/r2/r2/r2/r2/r2/v1",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "http://172.18.255.204:3000/porch/porch-test",
					Directory: "",
				},
			},
			wantPkg:        "r2.r2.r2.r2.r2.r2",
			wantRef:        "v1",
			wantManagedRef: true,
		},
		{
			name: "real test case 12",
			upstream: &kptfileapi.Upstream{
				Git: &kptfileapi.Git{
					Repo:      "https://github.com/nephio-project/catalog.git",
					Directory: "nephio/core/workload-crds",
					Ref:       "nephio/core/workload-crds/v3.0.0",
				},
			},
			wantRepoSpec: &configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      "https://github.com/nephio-project/catalog",
					Directory: "",
				},
			},
			wantPkg:        "nephio.core.workload-crds",
			wantRef:        "v3.0.0",
			wantManagedRef: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoSpec, pkg, ref, managedRef, err := GetRepoPackageRefFromUpstream(tt.upstream)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, repoSpec)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantRepoSpec, repoSpec)
				assert.Equal(t, tt.wantPkg, pkg)
				assert.Equal(t, tt.wantRef, ref)
				assert.Equal(t, tt.wantManagedRef, managedRef)
			}
		})
	}
}
