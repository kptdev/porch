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

// Reproduction tests for packageMetadata key deletion.
//
// These assert the desired behaviour and fail against the current
// implementation, so they are skipped. Remove the skips as part of the fix that
// makes the two sync directions agree on deletion.
//
// The two sync directions disagree on deletion semantics:
//
//	spec -> Kptfile (applyMetadataMap)      merge only, never deletes
//	Kptfile -> spec (updateKptfileFields)   full replace, deletes
//
// v1alpha1 has both modes: pkg/task/generictaskhandler.go PatchKptfile calls
// applyMetadataToKptfile(kf, obj, true) with replace semantics on the update
// path, and false on the create path. v1alpha2 only ever merges.

package packagerevision

import (
	"context"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// kptfileWithMetadata builds a Kptfile carrying the given labels and annotations.
func kptfileWithMetadata(labels, annotations map[string]string) kptfilev1.KptFile {
	return kptfilev1.KptFile{
		ResourceMeta: yaml.ResourceMeta{
			ObjectMeta: yaml.ObjectMeta{
				Labels:      labels,
				Annotations: annotations,
			},
		},
	}
}

// TestSpecKeyRemovalPropagatesToKptfile covers defect 1: a user removing a key
// from spec.packageMetadata should remove it from the Kptfile. Today
// applyMetadataMap only adds and overwrites, so the key survives.
func TestSpecKeyRemovalPropagatesToKptfile(t *testing.T) {
	kf := kptfileWithMetadata(
		map[string]string{"keep": "yes", "remove-me": "still-here"},
		map[string]string{"keep-anno": "yes", "remove-anno": "still-here"},
	)

	// User has dropped "remove-me" / "remove-anno" from the CR spec.
	pr := newTestPR(
		withLifecycle(porchv1alpha2.PackageRevisionLifecycleDraft),
		withMetadata(
			map[string]string{"keep": "yes"},
			map[string]string{"keep-anno": "yes"},
		),
	)

	changed := applyPackageMetadataToKptfile(&kf, pr)

	assert.True(t, changed, "removing a key from spec should be a change to apply")
	assert.Equal(t, map[string]string{"keep": "yes"}, kf.Labels,
		"label removed from spec.packageMetadata should be removed from the Kptfile")
	assert.Equal(t, map[string]string{"keep-anno": "yes"}, kf.Annotations,
		"annotation removed from spec.packageMetadata should be removed from the Kptfile")
}

// TestSpecKeyRemovalIsNotRevertedByKptfileSync covers the second half of
// defect 1: because the Kptfile keeps the dropped key, the next render syncs it
// straight back into spec, silently undoing the user's edit.
func TestSpecKeyRemovalIsNotRevertedByKptfileSync(t *testing.T) {
	kf := kptfileWithMetadata(map[string]string{"keep": "yes", "remove-me": "still-here"}, nil)

	pr := newTestPR(
		withLifecycle(porchv1alpha2.PackageRevisionLifecycleDraft),
		withMetadata(map[string]string{"keep": "yes"}, nil),
	)

	// Step 1: spec -> Kptfile. Should drop "remove-me" from the Kptfile.
	applyPackageMetadataToKptfile(&kf, pr)

	// Step 2: Kptfile -> spec, as run post-render by updateKptfileFields.
	synced := porchv1alpha2.KptfileToPackageMetadata(kf)

	assert.NotContains(t, synced.Labels, "remove-me",
		"Kptfile->spec sync must not resurrect a label the user removed from spec")
	assert.True(t, packageMetadataEqual(pr.Spec.PackageMetadata, synced),
		"spec and Kptfile must converge after one round trip")
}

// TestUpdateKptfileFieldsClearsMetadataWhenKptfileEmptied verifies that
// an empty Kptfile results in no spec patch (early return). Stale metadata
// is cleared by the CRD→Kptfile sync path (reconcilePackageMetadata), not here.
func TestUpdateKptfileFieldsClearsMetadataWhenKptfileEmptied(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	patched := false

	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
		}).Return(nil).Maybe()

	r := &PackageRevisionReconciler{Client: mockClient}

	// spec carries metadata that was synced from an earlier Kptfile revision.
	pr := basePR()
	pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
		Labels: map[string]string{"stale": "value"},
	}

	// The Kptfile has since had all labels and annotations removed.
	// Empty Kptfile → gates=nil, meta=nil, conds=nil → early return, no patch.
	r.updateKptfileFields(t.Context(), pr, kptfilev1.KptFile{})

	assert.False(t, patched, "empty Kptfile should not trigger a spec patch (stale metadata cleared by reconcilePackageMetadata)")
}
