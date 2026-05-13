// Copyright 2022, 2024-2025 The kpt Authors
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
	"strings"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func createFakePackageRevision(t *testing.T, kf kptfilev1.KptFile) *fake.FakePackageRevision {
	t.Helper()

	kfYaml, err := yaml.Marshal(kf)
	assert.NoError(t, err)

	resources := map[string]string{
		kptfilev1.KptFileName: string(kfYaml),
	}

	return &fake.FakePackageRevision{
		Kptfile: kf,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: resources,
			},
		},
	}
}

func TestKptfilePatch(t *testing.T) {
	newBaseKptfile := func() kptfilev1.KptFile {
		var k kptfilev1.KptFile
		k.APIVersion, k.Kind = kptfilev1.KptFileGVK().ToAPIVersionAndKind()
		k.Name = "pkg"
		return k
	}

	correctApiVersion, correctKind := kptfilev1.KptFileGVK().ToAPIVersionAndKind()

	testCases := map[string]struct {
		repoPkgRev   repository.PackageRevision
		newApiPkgRev *porchapi.PackageRevision
		shouldChange bool
		newKptfile   *kptfilev1.KptFile
	}{
		"no gates or conditions": {
			repoPkgRev: createFakePackageRevision(t, newBaseKptfile()),
			newApiPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{},
			},
			shouldChange: false,
		},
		"first gate and condition added": {
			repoPkgRev: createFakePackageRevision(t, newBaseKptfile()),
			newApiPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					ReadinessGates: []porchapi.ReadinessGate{
						{
							ConditionType: "foo",
						},
					},
				},
				Status: porchapi.PackageRevisionStatus{
					Conditions: []porchapi.Condition{
						{
							Type:   "foo",
							Status: porchapi.ConditionTrue,
						},
					},
				},
			},
			shouldChange: true,
			newKptfile: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					TypeMeta: yaml.TypeMeta{
						APIVersion: correctApiVersion,
						Kind:       correctKind,
					},
					ObjectMeta: yaml.ObjectMeta{
						NameMeta: yaml.NameMeta{
							Name: "pkg",
						},
					},
				},
				Info: &kptfilev1.PackageInfo{
					ReadinessGates: []kptfilev1.ReadinessGate{
						{
							ConditionType: "foo",
						},
					},
				},
				Status: &kptfilev1.Status{
					Conditions: []kptfilev1.Condition{
						{
							Type:   "foo",
							Status: "True",
						},
					},
				},
			},
		},
		"additional readinessGates and conditions added": {
			repoPkgRev: createFakePackageRevision(t, kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					TypeMeta:   yaml.TypeMeta{APIVersion: correctApiVersion, Kind: correctKind},
					ObjectMeta: yaml.ObjectMeta{NameMeta: yaml.NameMeta{Name: "pkg"}},
				},
				Info: &kptfilev1.PackageInfo{
					ReadinessGates: []kptfilev1.ReadinessGate{
						{
							ConditionType: "foo",
						},
					},
				},
				Status: &kptfilev1.Status{
					Conditions: []kptfilev1.Condition{
						{
							Type:   "foo",
							Status: kptfilev1.ConditionTrue,
						},
					},
				},
			}),
			newApiPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					ReadinessGates: []porchapi.ReadinessGate{
						{
							ConditionType: "foo",
						},
						{
							ConditionType: "bar",
						},
					},
				},
				Status: porchapi.PackageRevisionStatus{
					Conditions: []porchapi.Condition{
						{
							Type:    "foo",
							Status:  porchapi.ConditionTrue,
							Reason:  "reason",
							Message: "message",
						},
						{
							Type:    "bar",
							Status:  porchapi.ConditionFalse,
							Reason:  "reason",
							Message: "message",
						},
					},
				},
			},
			shouldChange: true,
			newKptfile: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					TypeMeta: yaml.TypeMeta{
						APIVersion: correctApiVersion,
						Kind:       correctKind,
					},
					ObjectMeta: yaml.ObjectMeta{
						NameMeta: yaml.NameMeta{
							Name: "pkg",
						},
					},
				},
				Info: &kptfilev1.PackageInfo{
					ReadinessGates: []kptfilev1.ReadinessGate{
						{
							ConditionType: "foo",
						},
						{
							ConditionType: "bar",
						},
					},
				},
				Status: &kptfilev1.Status{
					Conditions: []kptfilev1.Condition{
						{
							Type:    "foo",
							Status:  "True",
							Reason:  "reason",
							Message: "message",
						},
						{
							Type:    "bar",
							Status:  "False",
							Reason:  "reason",
							Message: "message",
						},
					},
				},
			},
		},
		"no changes": {
			repoPkgRev: createFakePackageRevision(t, kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					TypeMeta: yaml.TypeMeta{
						APIVersion: correctApiVersion,
						Kind:       correctKind,
					},
					ObjectMeta: yaml.ObjectMeta{
						NameMeta: yaml.NameMeta{
							Name: "pkg",
						},
					},
				},
				Info: &kptfilev1.PackageInfo{
					ReadinessGates: []kptfilev1.ReadinessGate{
						{
							ConditionType: "foo",
						},
						{
							ConditionType: "bar",
						},
					},
				},
				Status: &kptfilev1.Status{
					Conditions: []kptfilev1.Condition{
						{
							Type:    "foo",
							Status:  kptfilev1.ConditionTrue,
							Reason:  "reason",
							Message: "message",
						},
						{
							Type:    "bar",
							Status:  kptfilev1.ConditionFalse,
							Reason:  "reason",
							Message: "message",
						},
					},
				},
			}),
			newApiPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					ReadinessGates: []porchapi.ReadinessGate{
						{
							ConditionType: "foo",
						},
						{
							ConditionType: "bar",
						},
					},
				},
				Status: porchapi.PackageRevisionStatus{
					Conditions: []porchapi.Condition{
						{
							Type:    "foo",
							Status:  porchapi.ConditionTrue,
							Reason:  "reason",
							Message: "message",
						},
						{
							Type:    "bar",
							Status:  porchapi.ConditionFalse,
							Reason:  "reason",
							Message: "message",
						},
					},
				},
			},
			shouldChange: false,
		},
		"readinessGates and conditions removed": {
			repoPkgRev: createFakePackageRevision(t, kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					TypeMeta: yaml.TypeMeta{
						APIVersion: correctApiVersion,
						Kind:       correctKind,
					},
					ObjectMeta: yaml.ObjectMeta{
						NameMeta: yaml.NameMeta{
							Name: "pkg",
						},
					},
				},
				Info: &kptfilev1.PackageInfo{
					ReadinessGates: []kptfilev1.ReadinessGate{
						{
							ConditionType: "foo",
						},
						{
							ConditionType: "bar",
						},
					},
				},
				Status: &kptfilev1.Status{
					Conditions: []kptfilev1.Condition{
						{
							Type:    "foo",
							Status:  kptfilev1.ConditionTrue,
							Reason:  "reason",
							Message: "message",
						},
						{
							Type:    "bar",
							Status:  kptfilev1.ConditionFalse,
							Reason:  "reason",
							Message: "message",
						},
					},
				},
			}),
			newApiPkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					ReadinessGates: []porchapi.ReadinessGate{
						{
							ConditionType: "foo",
						},
					},
				},
				Status: porchapi.PackageRevisionStatus{
					Conditions: []porchapi.Condition{
						{
							Type:   "foo",
							Status: porchapi.ConditionTrue,
						},
					},
				},
			},
			shouldChange: true,
			newKptfile: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					TypeMeta: yaml.TypeMeta{
						APIVersion: correctApiVersion,
						Kind:       correctKind,
					},
					ObjectMeta: yaml.ObjectMeta{
						NameMeta: yaml.NameMeta{
							Name: "pkg",
						},
					},
				},
				Info: &kptfilev1.PackageInfo{
					ReadinessGates: []kptfilev1.ReadinessGate{
						{
							ConditionType: "foo",
						},
					},
				},
				Status: &kptfilev1.Status{
					Conditions: []kptfilev1.Condition{
						{
							Type:   "foo",
							Status: kptfilev1.ConditionTrue,
						},
					},
				},
			},
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {
			newKfContent, changed, err := PatchKptfile(context.Background(), tc.repoPkgRev, tc.newApiPkgRev)
			if err != nil {
				t.Fatal(err)
			}

			if tc.shouldChange {
				assert.True(t, changed)
				assert.NotEmpty(t, newKfContent)
				parsed, err := kptfileutil.DecodeKptfile(strings.NewReader(newKfContent))
				assert.NoError(t, err)
				assert.Equal(t, tc.newKptfile, parsed)
			} else {
				assert.False(t, changed)
				assert.Empty(t, newKfContent)
			}
		})
	}
}

func TestKptfilePatchPreservesComments(t *testing.T) {
	initialYAML := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-package
  # Package level comment
  labels:
    # Existing label comment
    existing-label: old-value
    # Keep this label comment
    keep-label: keep-value
  annotations:
    # Existing annotation comment
    existing.annotation: old-value
    # Keep this annotation comment
    keep.annotation: keep-value
# Comment before info section
info:
  # Comment about readiness gates
  readinessGates:
  - conditionType: foo # inline comment about foo
  # Comment about another gate that will be removed
  - conditionType: existing-gate
# Comment before status
status:
  # Comment about conditions
  conditions:
  - type: foo
    status: "True"
    # Comment about foo reason
    reason: initial-foo
    message: initial foo message
  - type: existing-condition
    status: "False"
    # Comment about existing reason that will be removed
    reason: initial-existing
    message: initial existing message
`

	repoPkgRev := &fake.FakePackageRevision{
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					kptfilev1.KptFileName: initialYAML,
				},
			},
		},
	}

	newApiPkgRev := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageMetadata: &porchapi.PackageMetadata{
				Labels: map[string]string{
					"existing-label": "updated-value",
					"keep-label":     "keep-value",
					"new-label":      "new-value",
				},
				Annotations: map[string]string{
					"existing.annotation": "updated-value",
					"keep.annotation":     "keep-value",
					"new.annotation":      "new-value",
				},
			},
			ReadinessGates: []porchapi.ReadinessGate{
				{ConditionType: "foo"},
				{ConditionType: "new-gate"},
			},
		},
		Status: porchapi.PackageRevisionStatus{
			Conditions: []porchapi.Condition{
				{
					Type:    "foo",
					Status:  porchapi.ConditionTrue,
					Reason:  "updated-foo",
					Message: "updated foo message",
				},
				{
					Type:    "new-condition",
					Status:  porchapi.ConditionFalse,
					Reason:  "new-reason",
					Message: "new message",
				},
			},
		},
	}

	actualYAML, changed, err := PatchKptfile(context.Background(), repoPkgRev, newApiPkgRev)
	assert.NoError(t, err)
	assert.True(t, changed)
	assert.NotEmpty(t, actualYAML)

	assert.Contains(t, actualYAML, "# Package level comment")
	assert.Contains(t, actualYAML, "# Comment before info section")
	assert.Contains(t, actualYAML, "# Comment about readiness gates")
	assert.Contains(t, actualYAML, "# inline comment about foo")
	assert.Contains(t, actualYAML, "# Comment before status")
	assert.Contains(t, actualYAML, "# Comment about conditions")
	assert.Contains(t, actualYAML, "# Comment about foo reason")

	assert.Contains(t, actualYAML, "# Existing label comment")
	assert.Contains(t, actualYAML, "# Keep this label comment")
	assert.Contains(t, actualYAML, "# Existing annotation comment")

	assert.NotContains(t, actualYAML, "# Comment about another gate that will be removed")
	assert.NotContains(t, actualYAML, "# Comment about existing reason that will be removed")

	assert.Contains(t, actualYAML, "existing-label: updated-value")
	assert.Contains(t, actualYAML, "new-label: new-value")
	assert.Contains(t, actualYAML, "existing.annotation: updated-value")
	assert.Contains(t, actualYAML, "new.annotation: new-value")
	assert.Contains(t, actualYAML, "conditionType: new-gate")
	assert.NotContains(t, actualYAML, "conditionType: existing-gate")
	assert.Contains(t, actualYAML, "reason: updated-foo")
	assert.Contains(t, actualYAML, "type: new-condition")
	assert.NotContains(t, actualYAML, "type: existing-condition")
}
