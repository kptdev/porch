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

package api

import (
	"fmt"
	"maps"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapiv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	starlarkImage  = "ghcr.io/kptdev/krm-functions-catalog/starlark:latest"
	setLabelsImage = "ghcr.io/kptdev/krm-functions-catalog/set-labels:latest"

	// note: to take effect, must be applied as annotation directly on PackageRevision (not in PackageRevisionResources's Kptfile):
	porchPushOnRenderFailure = "porch.kpt.dev/push-on-render-failure"
	subpackageDir1           = "my-subpackage-1"
	subpackageDir2           = "my-subpackage-2"
	subpackageDir3           = "my-subpackage-3"
)

var (
	kptSaveOnRenderFailure   = maps.All(map[string]string{"kpt.dev/save-on-render-failure": "true"})
	kptNoSaveOnRenderFailure = maps.All(map[string]string{"kpt.dev/save-on-render-failure": "false"})
	kptBFSRenderOrder        = maps.All(map[string]string{"kpt.dev/bfs-rendering": "true"})
	kptDFSRenderOrder        = maps.All(map[string]string{"kpt.dev/bfs-rendering": "false"})
)

// Setup:
//   - 3 subpackages: 1, 2, and 3 (to be discovered in that order when rendering)
//   - kpt.dev/save-on-render-failure: "true" in parent package's Kptfile
//   - kpt.dev/bfs-rendering: "true" in parent package's Kptfile
//
// When:
//   - subpackage 1 renders OK
//   - subpackage 2 renders partially, then encounters an error
//
// Then:
//   - modifications to subpackage 1 are preserved
//   - modifications to subpackage 2 before the error are preserved
//   - modifications to subpackage 2 after the error are not preserved
//   - modifications to subpackage 3 are neither performed nor preserved
func (t *PorchSuite) TestSaveOnFailureSubpackageRenderHandling() {
	const repo = "subpkg-save-on-fail"
	var (
		saveOnRenderFailure = kptSaveOnRenderFailure
		renderOrder         = kptBFSRenderOrder
	)
	parentPR := t.setupSubpackageRenderingTestScenario(repo, subpackageDir1, subpackageDir2, subpackageDir3)

	parentPR.Annotations = map[string]string{porchPushOnRenderFailure: "true"}
	t.UpdateF(parentPR)

	err := t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		// Modify my-subpackage-1's Kptfile to set a label on my-subpackage-1's resources
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: kptfilev1.KptFileGVK().Kind,
				Name: subpackageDir1,
			}},
			ConfigMap: map[string]string{
				"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
}])
				`,
			},
		})

		// Modify my-subpackage-2's Kptfile to set a label on my-subpackage-2's resources, then error out and interrupt
		// the root-plus-subpackages render
		injectErrorInSubpackageKptfile(subpackageDir2, kptfile)

		// Modify my-subpackage-3's Kptfile to set a label on my-subpackage-3's resources
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: kptfilev1.KptFileGVK().Kind,
				Name: subpackageDir3,
			}},
			ConfigMap: map[string]string{
				"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
}])
				`,
			},
		})
	})
	assert.NoError(t, err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")

	// Modify the parent package's Kptfile to set save-on-render-failure and render order annotations
	// (this also triggers the render which will encounter the error part-way through)
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, saveOnRenderFailure)
		maps.Insert(kptfile.Annotations, renderOrder)
	})

	assert.ErrorContains(t, err, "Deliberate error!")
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	// Modifications to subpackage 1 are present
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	// Modifications to subpackage 2 before the erroring function are present
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	// Modifications to subpackage 2 after the erroring function are not present
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test-2: modification-2")
	// Modifications to subpackage 3 are not present
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
}

// Setup:
//   - 3 subpackages: 1, 2, and 3 (to be discovered in that order when rendering)
//   - kpt.dev/save-on-render-failure: "false" in parent package's Kptfile
//   - kpt.dev/bfs-rendering: "true" in parent package's Kptfile
//
// When:
//   - subpackage 1 renders OK
//   - subpackage 2 renders partially, then encounters an error
//
// Then:
//   - no subpackage modifications are preserved
func (t *PorchSuite) TestNoSaveOnFailureSubpackageRenderHandling() {
	const repo = "subpkg-no-save-on-fail"
	var (
		saveOnRenderFailure = kptNoSaveOnRenderFailure
		renderOrder         = kptBFSRenderOrder
	)
	parentPR := t.setupSubpackageRenderingTestScenario(repo, subpackageDir1, subpackageDir2, subpackageDir3)

	parentPR.Annotations = map[string]string{porchPushOnRenderFailure: "true"}
	t.UpdateF(parentPR)
	err := t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		// Modify my-subpackage-1's Kptfile to set a label on my-subpackage-1's resources
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: kptfilev1.KptFileGVK().Kind,
				Name: subpackageDir1,
			}},
			ConfigMap: map[string]string{
				"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
}])
				`,
			},
		})

		// Modify my-subpackage-2's Kptfile to set a label on my-subpackage-2's resources, then error out and interrupt
		// the root-plus-subpackages render
		injectErrorInSubpackageKptfile(subpackageDir2, kptfile)

		// Modify my-subpackage-3's Kptfile to set a label on my-subpackage-3's resources
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: kptfilev1.KptFileGVK().Kind,
				Name: subpackageDir3,
			}},
			ConfigMap: map[string]string{
				"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
}])
				`,
			},
		})
	})
	assert.NoError(t, err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")

	// Modify the parent package's Kptfile to set save-on-render-failure and render order annotations
	// (this also triggers the render which will encounter the error part-way through)
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, saveOnRenderFailure)
		maps.Insert(kptfile.Annotations, renderOrder)
	})
	assert.ErrorContains(t, err, "Deliberate error!")

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	// No modifications are present
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test-2: modification-2")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
}

// Setup:
//   - 3 subpackages: 1, 2, and 3 (to be discovered in that order when rendering)
//   - kpt.dev/save-on-render-failure: "true" in parent package's Kptfile
//   - kpt.dev/bfs-rendering: "true" in parent package's Kptfile
//
// When:
//   - all subpackages render OK
//   - parent package renders partially, then encounters an error
//
// Then:
//   - modifications to subpackages are all preserved
//   - modifications to parent package before the error are preserved
//   - modifications to parent package after the error are not preserved
func (t *PorchSuite) TestSaveOnParentFailureSubpackageRenderHandling() {
	const repo = "subpkg-save-on-parent-fail"
	var (
		saveOnRenderFailure = kptSaveOnRenderFailure

		// BFS render order ensures subpackages are processed BEFORE parent package
		renderOrder = kptBFSRenderOrder
	)

	parentPR := t.setupSubpackageRenderingTestScenario(repo, subpackageDir1, subpackageDir2, subpackageDir3)

	parentPR.Annotations = map[string]string{porchPushOnRenderFailure: "true"}
	t.UpdateF(parentPR)
	err := t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		for _, subpkg := range []string{subpackageDir1, subpackageDir2, subpackageDir3} {
			kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
				Image: starlarkImage,
				Selectors: []kptfilev1.Selector{{
					Kind: kptfilev1.KptFileGVK().Kind,
					Name: subpkg,
				}},
				ConfigMap: map[string]string{
					"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
}])
					`,
				},
			})
		}
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: kptfilev1.KptFileGVK().Kind,
				Name: parentPackageName,
			}},
			ConfigMap: map[string]string{
				"source": `
rootKptfile = ctx.resource_list["items"][0]
rootKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
},{
	"image": "` + starlarkImage + `",
	"configMap": {
		"source": """
print(\"Deliberate error!\")
i = 10/0
"""
	}
},{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test-2": "modification-2"
	},
}])
				`,
			},
		})
	})
	assert.NoError(t, err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")

	// Modify the parent package's Kptfile to set save-on-render-failure and render order annotations
	// (this also triggers the render which will encounter the error part-way through)
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, saveOnRenderFailure)
		maps.Insert(kptfile.Annotations, renderOrder)
	})
	assert.ErrorContains(t, err, "Deliberate error!")
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	// Modifications to subpackages are all present
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1", "my-subpackage-1 does not contain the subpackage-render-test label")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1", "my-subpackage-2 does not contain the subpackage-render-test label")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1", "my-subpackage-3 does not contain the subpackage-render-test label")

	// Modifications to parent package before the erroring function are present
	assert.Contains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "subpackage-render-test: modification-1", "%s does not contain the subpackage-render-test label", parentPackageName)
	// Modifications to parent package after the erroring function are not present
	assert.NotContains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "subpackage-render-test-2: modification-2", "%s contains the subpackage-render-test-2 label", parentPackageName)
}

// Setup:
//   - 3 subpackages: 1, 2, and 3 (to be discovered in that order when rendering)
//   - kpt.dev/save-on-render-failure: "false" in parent package's Kptfile
//   - kpt.dev/bfs-rendering: "false" (DFS render order) in parent package's Kptfile
//
// When:
//   - all subpackages render OK
//   - parent package renders partially, then encounters an error
//
// Then:
//   - no subpackage modifications are preserved
//   - no parent package modifications are preserved
func (t *PorchSuite) TestNoSaveOnParentFailureSubpackageRenderHandling() {
	const repo = "subpkg-no-save-on-parent-fail"
	var (
		saveOnRenderFailure = kptNoSaveOnRenderFailure

		// DFS render order ensures parent package is processed BEFORE subpackages
		renderOrder = kptDFSRenderOrder
	)
	parentPR := t.setupSubpackageRenderingTestScenario(repo, subpackageDir1, subpackageDir2, subpackageDir3)

	err := t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		for _, subpkg := range []string{subpackageDir1, subpackageDir2, subpackageDir3} {
			kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
				Image: starlarkImage,
				Selectors: []kptfilev1.Selector{{
					Kind: kptfilev1.KptFileGVK().Kind,
					Name: subpkg,
				}},
				ConfigMap: map[string]string{
					"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
}])
					`,
				},
			})
		}
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: kptfilev1.KptFileGVK().Kind,
				Name: parentPackageName,
			}},
			ConfigMap: map[string]string{
				"source": `
rootKptfile = ctx.resource_list["items"][0]
rootKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
},{
	"image": "` + starlarkImage + `",
	"configMap": {
		"source": """
print(\"Deliberate error!\")
i = 10/0
"""
	}
},{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-2"
	},
}])
				`,
			},
		})
	})
	assert.NoError(t, err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")

	// Modify the parent package's Kptfile to set save-on-render-failure and render order annotations
	// (this also triggers the render which will encounter the error part-way through)
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, saveOnRenderFailure)
		maps.Insert(kptfile.Annotations, renderOrder)
	})
	assert.ErrorContains(t, err, "Deliberate error!")
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	// No modifications are present
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir2+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources[subpackageDir3+"/my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "subpackage-render-test: modification-1")
	assert.NotContains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "subpackage-render-test: modification-2")
}

// Setup:
//   - 1 subpackage with a timestamp-setting mutator in its pipeline
//   - parent package, also with a timestamp-setting mutator in its pipeline
//
// When: rendering is triggered first without explicit order (default DFS)
// Then: parent is rendered AFTER subpackage
//
// When: rendering is then triggered with kpt.dev/bfs-rendering: "true"
// Then: parent is rendered BEFORE subpackage
func (t *PorchSuite) TestBreadthFirstOrderSubpackageRenderHandling() {
	const repo = "subpkg-bfs"
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR1V1 := t.createPR(repo, cloneePackageName+"-1", clonedWorkspaceV1)
	err := t.UpdateKptfile(cloneePR1V1, func(kptfile *kptfilev1.KptFile) {
		// Set subpackage's render order to BFS
		maps.Insert(kptfile.Annotations, kptBFSRenderOrder)

		// Add a script in subpackage to set a timestamp and track time of render
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: "ConfigMap",
			}},
			ConfigMap: map[string]string{
				"source": `
load('time.star', 'time')
file = ctx.resource_list["items"][0]
file.get("metadata", {}).get("labels", {})["subpackage-render-test-timestamp"] = time.now()
				`,
			},
		})
	})
	require.NoError(t.T(), err)
	t.approvePR(cloneePR1V1)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR1V1, subpackageDir1)
	assert.NoError(t, err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR1V1, parentPR, subpackageDir1, err)

	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		// Add a script in parent package to set a timestamp and track time of render
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: "ConfigMap",
				Name: fmt.Sprintf("my-%s.%s.%s-configmap", repo, parentPackageName, parentWorkspace),
			}},
			ConfigMap: map[string]string{
				"source": `
load('time.star', 'time')
file = ctx.resource_list["items"][0]
file.get("metadata", {}).get("labels", {})["subpackage-render-test-timestamp"] = time.now()
				`,
			},
		})
	})
	require.NoError(t.T(), err)

	// Insert a meaningless annotation in parent package's Kptfile to trigger a render
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, maps.All(map[string]string{"something": "some-value"}))
	})
	require.NoError(t.T(), err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)
	parentRenderTime := t.extractRenderTimestamp(parentPRResources, "my-configmap.yaml")
	subpackageRenderTime := t.extractRenderTimestamp(parentPRResources, subpackageDir1+"/my-configmap.yaml")

	// Greater parent render time shows that parent was rendered AFTER subpackage,
	// showing that BFS annotation in subpackage had no effect in render done through
	// parent package.
	// (also showing that DFS rendering is default)
	assert.Greater(t, parentRenderTime, subpackageRenderTime)

	// Modify the parent package's Kptfile to set the render order annotation
	// (this also triggers the render)
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, kptBFSRenderOrder)
	})
	require.NoError(t.T(), err)

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)
	parentRenderTime = t.extractRenderTimestamp(parentPRResources, "my-configmap.yaml")
	subpackageRenderTime = t.extractRenderTimestamp(parentPRResources, subpackageDir1+"/my-configmap.yaml")

	// Lesser parent render time shows that parent was rendered BEFORE subpackage
	assert.Less(t, parentRenderTime, subpackageRenderTime)
}

// Setup:
//   - 1 subpackage with a timestamp-setting mutator in its pipeline
//   - parent package, also with a timestamp-setting mutator in its pipeline
//
// When: rendering is triggered first without explicit order (default DFS)
// Then: parent is rendered AFTER subpackage
//
// When: rendering is then triggered with kpt.dev/bfs-rendering: "false" (explicit DFS)
// Then: parent is still rendered AFTER subpackage
func (t *PorchSuite) TestDepthFirstOrderSubpackageRenderHandling() {
	const repo = "subpkg-dfs"
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR1V1 := t.createPR(repo, cloneePackageName+"-1", clonedWorkspaceV1)
	err := t.UpdateKptfile(cloneePR1V1, func(kptfile *kptfilev1.KptFile) {
		// Set subpackage's render order to DFS
		maps.Insert(kptfile.Annotations, kptDFSRenderOrder)

		// Add a script in subpackage to set a timestamp and track time of render
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: "ConfigMap",
			}},
			ConfigMap: map[string]string{
				"source": `
load('time.star', 'time')
file = ctx.resource_list["items"][0]
file.get("metadata", {}).get("labels", {})["subpackage-render-test-timestamp"] = time.now()
				`,
			},
		})
	})
	require.NoError(t.T(), err)
	t.approvePR(cloneePR1V1)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR1V1, subpackageDir1)
	assert.NoError(t, err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR1V1, parentPR, subpackageDir1, err)

	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		// Add a script in parent package to set a timestamp and track time of render
		kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
			Image: starlarkImage,
			Selectors: []kptfilev1.Selector{{
				Kind: "ConfigMap",
				Name: fmt.Sprintf("my-%s.%s.%s-configmap", repo, parentPackageName, parentWorkspace),
			}},
			ConfigMap: map[string]string{
				"source": `
load('time.star', 'time')
file = ctx.resource_list["items"][0]
file.get("metadata", {}).get("labels", {})["subpackage-render-test-timestamp"] = time.now()
				`,
			},
		})
	})
	require.NoError(t.T(), err)

	// Insert a meaningless annotation in parent package's Kptfile to trigger a render
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, maps.All(map[string]string{"something": "some-value"}))
	})
	require.NoError(t.T(), err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)
	parentRenderTime := t.extractRenderTimestamp(parentPRResources, "my-configmap.yaml")
	subpackageRenderTime := t.extractRenderTimestamp(parentPRResources, subpackageDir1+"/my-configmap.yaml")

	// Greater parent render time shows that parent was rendered AFTER subpackage,
	// (also showing that DFS rendering is default)
	assert.Greater(t, parentRenderTime, subpackageRenderTime)

	// Modify the parent package's Kptfile to set the render order annotation
	// (this also triggers the render)
	err = t.UpdateKptfile(parentPR, func(kptfile *kptfilev1.KptFile) {
		maps.Insert(kptfile.Annotations, kptDFSRenderOrder)
	})
	require.NoError(t.T(), err)

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)
	parentRenderTime = t.extractRenderTimestamp(parentPRResources, "my-configmap.yaml")
	subpackageRenderTime = t.extractRenderTimestamp(parentPRResources, subpackageDir1+"/my-configmap.yaml")

	// Greater parent render time shows that parent was rendered AFTER subpackage
	assert.Greater(t, parentRenderTime, subpackageRenderTime)
}

func (t *PorchSuite) setupSubpackageRenderingTestScenario(repo string, subpackageDir1 string, subpackageDir2 string, subpackageDir3 string) *porchapiv1alpha1.PackageRevision {
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR1V1 := t.createPR(repo, cloneePackageName+"-1", clonedWorkspaceV1)
	t.approvePR(cloneePR1V1)

	cloneePR2V1 := t.createPR(repo, cloneePackageName+"-2", clonedWorkspaceV1)
	t.approvePR(cloneePR2V1)

	cloneePR3V1 := t.createPR(repo, cloneePackageName+"-3", clonedWorkspaceV1)
	t.approvePR(cloneePR3V1)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err := t.cloneSubpackage(parentPR, cloneePR1V1, subpackageDir1)
	require.NoError(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR1V1, parentPR, subpackageDir1, err)
	parentPR, err = t.cloneSubpackage(parentPR, cloneePR2V1, subpackageDir2)
	require.NoError(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR2V1, parentPR, subpackageDir2, err)
	parentPR, err = t.cloneSubpackage(parentPR, cloneePR3V1, subpackageDir3)
	require.NoError(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR3V1, parentPR, subpackageDir3, err)
	return parentPR
}

func injectErrorInSubpackageKptfile(subpackageDir string, kptfile *kptfilev1.KptFile) {
	kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
		Image: starlarkImage,
		Selectors: []kptfilev1.Selector{{
			Kind: kptfilev1.KptFileGVK().Kind,
			Name: subpackageDir,
		}},
		ConfigMap: map[string]string{
			"source": `
subpkgKptfile = ctx.resource_list["items"][0]
subpkgKptfile.get("pipeline", {}).get("mutators", []).extend([{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test": "modification-1"
	},
},{
	"image": "` + starlarkImage + `",
	"configMap": {
		"source": """
print(\"Deliberate error!\")
i = 10/0
"""
	}
},{
	"image": "` + setLabelsImage + `",
	"configMap": {
		"subpackage-render-test-2": "modification-2"
	},
}])
			`,
		},
	})
}

func (t *PorchSuite) extractRenderTimestamp(resources porchapiv1alpha1.PackageRevisionResources, resourcePath string) time.Time {
	t.T().Helper()
	resourceRawString := resources.Spec.Resources[resourcePath]
	r, err := yaml.Parse(resourceRawString)
	require.NoError(t.T(), err)
	renderTimestamp, err := r.GetFieldValue("metadata.labels.subpackage-render-test-timestamp")
	require.NoError(t.T(), err)
	renderTime, err := time.Parse(time.RFC3339, renderTimestamp.(string))
	require.NoError(t.T(), err)
	return renderTime
}
