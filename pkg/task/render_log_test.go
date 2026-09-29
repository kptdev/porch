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

package task

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
)

func TestRenderLogsPackageNamesFromKptfile(t *testing.T) {
	oldKlogState := klog.CaptureState()
	var logs bytes.Buffer
	klog.SetOutput(&logs)
	klog.LogToStderr(false)
	t.Cleanup(func() {
		klog.Flush()
		oldKlogState.Restore()
	})

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{Name: "my-repo"},
			Package: "parent-pkg",
		},
		WorkspaceName: "my-ws",
	}
	handler := &genericTaskHandler{
		runnerOptionsResolver: func(namespace string) runneroptions.RunnerOptions {
			ros := runneroptions.RunnerOptions{}
			ros.InitDefaults(runneroptions.GHCRImagePrefix)
			// Matches apiserver.KptLogOptions; renderMutation fills PkgNameFormat.
			ros.LogOptions = runneroptions.LogOptions{
				PkgNameSep: ".",
				PkgNameID:  runneroptions.KptfileMeta,
			}
			return ros
		},
		runtime: NewSimpleFunctionRuntime(),
	}

	_, _, err := handler.renderMutation("test-ns", prKey).apply(context.Background(), repository.PackageResources{
		Contents: map[string]string{
			"Kptfile":        testRenderKptfile("parent-pkg"),
			"cm.yaml":        testRenderConfigMap("parent"),
			"subdir/Kptfile": testRenderKptfile("child-pkg"),
			"subdir/cm.yaml": testRenderConfigMap("child"),
		},
	})
	require.NoError(t, err)
	klog.Flush()

	logged := logs.String()
	assert.Contains(t, logged, `Package "my-repo.parent-pkg.my-ws":`)
	assert.Contains(t, logged, `Package "my-repo.parent-pkg.child-pkg.my-ws":`)
}

func testRenderKptfile(name string) string {
	return fmt.Sprintf(`apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: %s
pipeline:
  mutators:
    - image: noop
`, name)
}

func testRenderConfigMap(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
data:
  key: value
`, name)
}
