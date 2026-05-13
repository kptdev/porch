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

package e2e

import (
	"flag"
	"os"
	"testing"
)

const (
	gcrioPrefix    = "ghcr.io/kptdev/krm-functions-catalog"
	gcrioPrefixEnv = "PORCH_GHCR_PREFIX_URL"
)

var (
	testName = flag.String("porch-cli-test-name", "", "The folder name of the test to run. Leaving empty will skip the test")
)

func Test(t *testing.T) {
	flag.Parse()
	if *testName == "" {
		t.Skipf("Skipping test because --porch-cli-test-name is not set")
	}

	testSuite := NewCliTestSuite(t, "./testdata")
	hackedTestCase := ReadTestCaseConfig(t, *testName, "./testdata/"+*testName)

	if defaultPrefix := os.Getenv(gcrioPrefixEnv); defaultPrefix != "" {
		testSuite.SearchAndReplace[gcrioPrefix] = defaultPrefix
	}

	testSuite.RunTestCase(t, hackedTestCase)
}
