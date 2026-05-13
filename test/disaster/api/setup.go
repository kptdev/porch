// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package disaster

import (
	"github.com/kptdev/porch/test/disaster/api/environment/shell"
	"github.com/kptdev/porch/test/e2e/suiteutils"
)

var (
	setupScript = "/test/disaster/deployment/setup.sh"
)

func setupEnv(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()
	if err := (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLine(t.PorchRoot + setupScript); err != nil {
		t.Fatalf("Error running %s: %v", t.PorchRoot+setupScript, err)
	}
}

func resetEnv(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()
	if err := (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLine(t.PorchRoot+setupScript, "reset"); err != nil {
		t.Fatalf("Error running %s: %v", t.PorchRoot+setupScript, err)
	}
}
