// Copyright 2022, 2024-2026 The kpt Authors
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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/pkg/repository"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/kustomize/kyaml/kio"
)

func TestRender(t *testing.T) {
	runnerOptions := runneroptions.RunnerOptions{}
	runnerOptions.InitDefaults(runneroptions.GHCRImagePrefix)

	render := &renderPackageMutation{
		runnerOptions: runnerOptions,
		runtime:       NewSimpleFunctionRuntime(),
	}

	testdata, err := filepath.Abs(filepath.Join(".", "testdata", "simple-render"))
	if err != nil {
		t.Fatalf("Failed to find testdata: %v", err)
	}
	packagePath := filepath.Join(testdata, "simple-bucket")
	r := &kio.LocalPackageReader{
		PackagePath:        packagePath,
		IncludeSubpackages: true,
		MatchFilesGlob:     append(kio.MatchAll, kptfilev1.KptFileName),
		FileSystem:         filesys.FileSystemOrOnDisk{},
	}

	w := &packageWriter{
		output: repository.PackageResources{
			Contents: map[string]string{},
		},
	}

	if err := (kio.Pipeline{Inputs: []kio.Reader{r}, Outputs: []kio.Writer{w}}).Execute(); err != nil {
		t.Fatalf("Failed to read package: %v", err)
	}

	rendered, _, err := render.apply(context.Background(), w.output)
	if err != nil {
		t.Errorf("package render failed: %v", err)
	}

	got, ok := rendered.Contents["bucket.yaml"]
	if !ok {
		t.Errorf("Cannot find output config (bucket.yaml) in %v", rendered.Contents)
	}

	want, err := os.ReadFile(filepath.Join(testdata, "expected.txt"))
	if err != nil {
		t.Fatalf("Cannot read expected.txt: %v", err)
	}

	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("Unexpected result (-want, +got): %s", diff)
	}
}
