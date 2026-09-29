package packagerevision

import (
	"context"
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldSkipSubpackageOperationNilOperation(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{}

	assert.True(t, r.shouldSkipSubpackageOperation(pr))
}

func TestShouldSkipSubpackageOperationNewOperation(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	assert.False(t, r.shouldSkipSubpackageOperation(pr))
}

func TestShouldSkipSubpackageOperationAlreadyExecuted(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}
	pr.Status.LastSubpackageOperationHash = r.getSubpackageOperationHash(pr)

	assert.True(t, r.shouldSkipSubpackageOperation(pr))
}

func TestShouldSkipSubpackageOperationDifferentHash(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			LastSubpackageOperationHash: "sha256:stale-hash",
		},
	}

	assert.False(t, r.shouldSkipSubpackageOperation(pr))
}

func TestGetSubpackageOperationHashDeterministic(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	hash1 := r.getSubpackageOperationHash(pr)
	hash2 := r.getSubpackageOperationHash(pr)
	assert.Equal(t, hash1, hash2)
	assert.Contains(t, hash1, "sha256:")
}

func TestGetSubpackageOperationHashNilOperation(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Status: porchv1alpha2.PackageRevisionStatus{
			LastSubpackageOperationHash: "sha256:previous-hash",
		},
	}

	hash := r.getSubpackageOperationHash(pr)
	assert.Equal(t, "sha256:previous-hash", hash)
}

func TestGetSubpackageOperationHashDifferentForDifferentOps(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr1 := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "subpkg-a",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}
	pr2 := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "subpkg-b",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	assert.NotEqual(t, r.getSubpackageOperationHash(pr1), r.getSubpackageOperationHash(pr2))
}

const minimalKptfile = `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-subpkg
info:
  description: test
status:
  conditions:
  - type: Ready
    status: "True"
`

func TestInsertSubpackageResourcesSuccess(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":     "parent-kptfile",
		"parent.yaml": "parent-content",
	}
	subpkgResources := map[string]string{
		"Kptfile":       minimalKptfile,
		"resource.yaml": "subpkg-resource",
	}

	result, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	require.NoError(t, err)
	assert.Equal(t, "parent-kptfile", result["Kptfile"])
	assert.Equal(t, "parent-content", result["parent.yaml"])
	assert.Equal(t, "subpkg-resource", result["my-subpkg/resource.yaml"])
	// Status should be cleared from the cloned subpackage Kptfile.
	assert.NotContains(t, result["my-subpkg/Kptfile"], "status:")
	assert.Contains(t, result["my-subpkg/Kptfile"], "name: my-subpkg")
}

func TestInsertSubpackageResourcesConflictExactPath(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":   "parent-kptfile",
		"my-subpkg": "a file at exactly the subpackage dir path",
	}
	subpkgResources := map[string]string{"Kptfile": "subpkg-kptfile"}

	_, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	assert.ErrorContains(t, err, "cannot clone subpackage into parent")
	assert.ErrorContains(t, err, "my-subpkg")
}

func TestInsertSubpackageResourcesConflict(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":                 "parent-kptfile",
		"my-subpkg/existing.yaml": "existing",
	}
	subpkgResources := map[string]string{
		"Kptfile": "subpkg-kptfile",
	}

	_, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	assert.ErrorContains(t, err, "cannot clone subpackage into parent")
	assert.ErrorContains(t, err, "my-subpkg")
}

func TestInsertSubpackageResourcesEmptySubpackage(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	parentResources := map[string]string{"Kptfile": "parent"}
	result, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Kptfile": "parent"}, result)
}

func TestUpgradeSubpackageResourcesSuccess(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				Upgrade:       &porchv1alpha2.PackageUpgradeSpec{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":            "parent-kptfile",
		"parent.yaml":        "parent-content",
		"my-subpkg/Kptfile":  "old-subpkg-kptfile",
		"my-subpkg/old.yaml": "old-content",
	}
	newSubpkgResources := map[string]string{
		"Kptfile":  "new-subpkg-kptfile",
		"new.yaml": "new-content",
	}

	result, err := r.upgradeSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, newSubpkgResources)
	require.NoError(t, err)
	assert.Equal(t, "parent-kptfile", result["Kptfile"])
	assert.Equal(t, "parent-content", result["parent.yaml"])
	assert.Equal(t, "new-subpkg-kptfile", result["my-subpkg/Kptfile"])
	assert.Equal(t, "new-content", result["my-subpkg/new.yaml"])
	assert.NotContains(t, result, "my-subpkg/old.yaml")
}

func TestUpgradeSubpackageResourcesNotFound(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "nonexistent",
				Upgrade:       &porchv1alpha2.PackageUpgradeSpec{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":     "parent-kptfile",
		"parent.yaml": "parent-content",
	}

	_, err := r.upgradeSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, map[string]string{"Kptfile": "new"})
	assert.ErrorContains(t, err, "does not have a subpackage at")
}

func TestUpsertSubpackageResourcesClonePath(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	parentResources := map[string]string{"Kptfile": "parent"}
	subpkgResources := map[string]string{"Kptfile": "subpkg"}

	result, err := r.upsertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	require.NoError(t, err)
	assert.Contains(t, result, "subpkg/Kptfile")
}

func TestUpsertSubpackageResourcesUpgradePath(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "subpkg",
				Upgrade:       &porchv1alpha2.PackageUpgradeSpec{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":         "parent",
		"subpkg/Kptfile":  "old",
		"subpkg/old.yaml": "old",
	}
	subpkgResources := map[string]string{"Kptfile": "new"}

	result, err := r.upsertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	require.NoError(t, err)
	assert.Equal(t, "new", result["subpkg/Kptfile"])
	assert.NotContains(t, result, "subpkg/old.yaml")
}

func TestUpsertSubpackageResourcesNoFieldsSet(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "subpkg",
			},
		},
	}

	_, err := r.upsertSubpackageResourcesInDraftResources(context.Background(), pr, map[string]string{}, map[string]string{})
	assert.ErrorContains(t, err, "no fields set")
}

func TestUpsertSubpackageResourcesSkippedWhenNil(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{}

	parentResources := map[string]string{"Kptfile": "parent"}
	result, err := r.upsertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, parentResources, result)
}

// --- Tests for parentSubpackageFound ---

func TestParentSubpackageFoundExactMatch(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "sub", r.parentSubpackageFound("sub", "sub/Kptfile"))
}

func TestParentSubpackageFoundParentOfTarget(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "sub", r.parentSubpackageFound("sub/nested", "sub/Kptfile"))
}

func TestParentSubpackageFoundDeeplyNested(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "a/b", r.parentSubpackageFound("a/b/c/d", "a/b/Kptfile"))
}

func TestParentSubpackageFoundNoMatchNonKptfile(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "", r.parentSubpackageFound("sub", "sub/resource.yaml"))
}

func TestParentSubpackageFoundNoMatchUnrelatedDir(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "", r.parentSubpackageFound("sub/nested", "other/Kptfile"))
}

func TestParentSubpackageFoundNoMatchSibling(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "", r.parentSubpackageFound("sub/nested", "sub/other/Kptfile"))
}

func TestParentSubpackageFoundNoMatchDeeper(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "", r.parentSubpackageFound("sub", "sub/nested/Kptfile"))
}

func TestParentSubpackageFoundNoMatchRootKptfile(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "", r.parentSubpackageFound("sub", "Kptfile"))
}

func TestParentSubpackageFoundNoMatchSimilarPrefix(t *testing.T) {
	r := &PackageRevisionReconciler{}
	assert.Equal(t, "", r.parentSubpackageFound("subpkg", "sub/Kptfile"))
}

// --- Tests for insert with parentSubpackageFound detection ---

func TestInsertSubpackageResourcesConflictWithParentSubpackage(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "sub/nested",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":     "parent-kptfile",
		"sub/Kptfile": "existing subpackage kptfile",
	}
	subpkgResources := map[string]string{"Kptfile": "new-subpkg"}

	_, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	assert.ErrorContains(t, err, "cannot clone subpackage into another subpackage")
	assert.ErrorContains(t, err, "sub")
}

func TestInsertSubpackageResourcesNoConflictWithDeeperKptfile(t *testing.T) {
	// A Kptfile deeper than the target subpackageDir should NOT conflict
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "sub",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":       "parent-kptfile",
		"other/Kptfile": "sibling subpackage",
	}
	subpkgResources := map[string]string{"Kptfile": "new-subpkg"}

	result, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, subpkgResources)
	require.NoError(t, err)
	assert.Contains(t, result, "sub/Kptfile")
}

// --- Tests for upgrade with new conflict detection ---

func TestUpgradeSubpackageResourcesExactMatchContent(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				Upgrade:       &porchv1alpha2.PackageUpgradeSpec{},
			},
		},
	}

	// Parent has a file named exactly "my-subpkg" (not a directory prefix)
	parentResources := map[string]string{
		"Kptfile":   "parent-kptfile",
		"my-subpkg": "some file exactly at the subpackage dir path",
	}

	_, err := r.upgradeSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, map[string]string{"Kptfile": "new"})
	assert.ErrorContains(t, err, "cannot upgrade subpackage in parent, parent already has content at")
}

func TestUpgradeSubpackageResourcesConflictWithParentSubpackage(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "sub/nested/target",
				Upgrade:       &porchv1alpha2.PackageUpgradeSpec{},
			},
		},
	}

	parentResources := map[string]string{
		"Kptfile":     "parent-kptfile",
		"sub/Kptfile": "parent subpackage kptfile",
	}

	_, err := r.upgradeSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, map[string]string{"Kptfile": "new"})
	assert.ErrorContains(t, err, "cannot upgrade subpackage in another subpackage")
	assert.ErrorContains(t, err, "sub")
}

func TestUpgradeSubpackageResourcesUpdatedErrorMessage(t *testing.T) {
	// Verify the updated error message uses "cannot find subpackage"
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "nonexistent",
				Upgrade:       &porchv1alpha2.PackageUpgradeSpec{},
			},
		},
	}

	parentResources := map[string]string{"Kptfile": "parent"}
	_, err := r.upgradeSubpackageResourcesInDraftResources(context.Background(), pr, parentResources, map[string]string{"Kptfile": "new"})
	assert.ErrorContains(t, err, "cannot find subpackage in parent")
	assert.ErrorContains(t, err, "nonexistent")
}

// --- Tests for applySubpackageOperation validation ---

func TestApplySubpackageOperationInvalidDir(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "/absolute-path",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	_, opType, err := r.applySubpackageOperation(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
	assert.Empty(t, opType)
}

func TestApplySubpackageOperationInvalidDirDoubleDots(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "../escape",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	_, opType, err := r.applySubpackageOperation(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
	assert.Empty(t, opType)
}

func TestApplySubpackageOperationNoFieldsSet(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "valid-dir",
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	_, _, err := r.applySubpackageOperation(context.Background(), pr)
	assert.ErrorContains(t, err, "has no fields set")
}

// TestSubpackageOperationIdempotentOnRepeat verifies that if the same SubpackageOperation
// is present on a PR across multiple consecutive reconcile invocations (e.g. the controller
// re-queues before the client clears the field), only the first invocation executes the
// operation and subsequent ones are silently skipped.
func TestSubpackageOperationIdempotentOnRepeat(t *testing.T) {
	r := &PackageRevisionReconciler{}

	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	parentResources := map[string]string{"Kptfile": "parent"}
	subpkgResources := map[string]string{"Kptfile": "subpkg-kptfile", "resource.yaml": "content"}

	// First invocation: operation not yet executed — should NOT be skipped.
	assert.False(t, r.shouldSkipSubpackageOperation(pr), "first invocation should not be skipped")

	result1, err := r.insertSubpackageResourcesInDraftResources(context.Background(), pr, copyMap(parentResources), subpkgResources)
	require.NoError(t, err)
	assert.Contains(t, result1, "my-subpkg/Kptfile")
	assert.Contains(t, result1, "my-subpkg/resource.yaml")

	// Simulate the controller writing the hash to status after successful execution.
	pr.Status.LastSubpackageOperationHash = r.getSubpackageOperationHash(pr)

	// Second invocation: same SubpackageOperation still on spec — should be skipped.
	assert.True(t, r.shouldSkipSubpackageOperation(pr), "second invocation with same operation should be skipped")

	// upsertSubpackageResourcesInDraftResources must return the parent unchanged on skip.
	parentWithSubpkg := copyMap(result1)
	result2, err := r.upsertSubpackageResourcesInDraftResources(context.Background(), pr, parentWithSubpkg, subpkgResources)
	require.NoError(t, err)
	assert.Equal(t, parentWithSubpkg, result2, "resources must be unchanged on second invocation")

	// Third invocation: still the same operation — still skipped.
	assert.True(t, r.shouldSkipSubpackageOperation(pr), "third invocation with same operation should be skipped")

	result3, err := r.upsertSubpackageResourcesInDraftResources(context.Background(), pr, copyMap(result1), subpkgResources)
	require.NoError(t, err)
	assert.Equal(t, result1, result3, "resources must be unchanged on third invocation")
}

// copyMap returns a shallow copy of m so tests don't mutate shared state.
func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
