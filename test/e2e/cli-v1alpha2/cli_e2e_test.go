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

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	clisuite "github.com/kptdev/porch/test/e2e/cli"
)

func TestPorchCLIV1Alpha2(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("set E2E to run this test")
	}

	suite := clisuite.NewCliTestSuite(t, filepath.Join(".", "testdata"))
	suite.DeleteNamespaceFunc = clisuite.KubectlDeleteNamespaceV1Alpha2
	suite.RunTests(t)
}
