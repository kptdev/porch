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

package packagerevision

import (
	"context"

	"github.com/kptdev/porch/api/porch"
	"github.com/kptdev/porch/api/porch/v1alpha2"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/internal/telemetry"
)

const (
	prTelemetryName  = telemetry.ResourcePackageRevision
	prrTelemetryName = telemetry.ResourcePackageRevisionResources
	praTelemetryName = telemetry.ResourcePackageRevisionApproval
)

// This file abstracts the lifecycle/telemetry wiring out of functional code so
// that reconcile logic reads cleanly and each measurement point is a single
// deferred call that automatically wraps all subsequent code in its function.
// Telemetry-specific logic (e.g. determining PackageRevision vs PackageRevisionApproval
// resource naming, resolving the lifecycle to report once the operation settles)
// should live here rather than in the hot path.

// measureControllerAPIOperation starts timing and in-flight tracking for a single Porch API
// operation triggered from the Porch package revision controller. It returns a finish function,
// which must be deferred on the caller side to ensure the end of the operation is recorded.
//
// v1alpha2 specialisation of telemetry.MeasureAPIOperation(ctx, op, resource, apiVersion, desiredLifecycle, prKey) operationFinishFunc;
// automatically converts v1alpha2.PackageRevisionLifecycle to porch.PackageRevisionLifecycle.
//
// Usage (single line in the caller):
//
//	defer telemetry.measureControllerAPIOperation(ctx, op, resource, desiredLifecycle, pr)(&err, nil)
//
// The op supplies both the verb (op.AllCaps) and, combined with resource, the
// porch operation name (op.TitleCase + resource). Pass a non-nil lifecycleAfter
// closure when the final recorded lifecycle differs from desiredLifecycle or must be
// computed from the final error.
func measureControllerAPIOperation(ctx context.Context, op telemetry.OperationVerbs, resource string, desiredLifecycle v1alpha2.PackageRevisionLifecycle, pr *v1alpha2.PackageRevision) func(errPtr *error, lifecycleAfter func() v1alpha2.PackageRevisionLifecycle) {
	prKey := telemetry.ParsePkgRevK8sName2Key(pr.Namespace, pr.Name)
	finishFunc := telemetry.MeasureAPIOperation(ctx, op, resource, telemetry.APIVersionV1Alpha2, porch.PackageRevisionLifecycle(desiredLifecycle), &prKey)

	convertedLifecycleFinishFunc := func(errPtr *error, lifecycleAfter func() v1alpha2.PackageRevisionLifecycle) {
		var porchProvider func() porch.PackageRevisionLifecycle
		if lifecycleAfter != nil {
			porchProvider = func() porch.PackageRevisionLifecycle {
				return porch.PackageRevisionLifecycle(lifecycleAfter())
			}
		}
		finishFunc(errPtr, porchProvider)
		// Request count is specific to controller operations (recorded under ControllerUser).
		telemetry.RecordControllerRequestCount(ctx, resource, op.AllCaps)
	}

	return convertedLifecycleFinishFunc
}

// v1alpha2-specific analogue of telemetry.OperationFinishFunc
type OperationFinishFunc func(errPtr *error)

// measurePkgRevGet times the content-cache read that gets a package revision.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevGet(ctx, pr)(&err)
func measurePkgRevGet(ctx context.Context, pr *porchv1alpha2.PackageRevision) OperationFinishFunc {
	finish := measureControllerAPIOperation(
		ctx, telemetry.Operations.Get, prTelemetryName, pr.Spec.Lifecycle, pr)
	return func(errPtr *error) { finish(errPtr, nil) }
}

// measurePkgRevResourcesGet times the content-cache read that retrieves a package
// revision's resources.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevResourcesGet(ctx, pr)(&err)
func measurePkgRevResourcesGet(ctx context.Context, pr *porchv1alpha2.PackageRevision) OperationFinishFunc {
	finish := measureControllerAPIOperation(
		ctx, telemetry.Operations.Get, prrTelemetryName, pr.Spec.Lifecycle, pr)
	return func(errPtr *error) { finish(errPtr, nil) }
}

// measureSourceOperation times a one-time source/subpackage draft operation.
//
// (err != nil && verb.AllCaps == telemetry.Operations.Create.AllCaps) detects
// when a CREATE operation encounters an error, in which case the package revision
// has not been properly create and we record an empty lifeycle_after
//
// Usage (single line in the caller):
//
//	defer measureSourceOperation(ctx, pr, verb, operationType)(&err)
func measureSourceOperation(
	ctx context.Context,
	pr *v1alpha2.PackageRevision,
	verb telemetry.OperationVerbs,
	operationType string,
) OperationFinishFunc {
	op := telemetry.ParseOperation(operationType)
	op.AllCaps = verb.AllCaps
	finish := measureControllerAPIOperation(
		ctx, op, prTelemetryName,
		v1alpha2.PackageRevisionLifecycleDraft, pr)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha2.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return func(err error) porchv1alpha2.PackageRevisionLifecycle {
				if (err != nil && verb.AllCaps == telemetry.Operations.Create.AllCaps) || pr == nil {
					return ""
				}
				return pr.Spec.Lifecycle
			}(err)
		})
	}
}

func measureCreateSourceOperation(ctx context.Context, pr *v1alpha2.PackageRevision, operationType string) OperationFinishFunc {
	return measureSourceOperation(ctx, pr, telemetry.Operations.Create, operationType)
}
func measureUpdateSourceOperation(ctx context.Context, pr *v1alpha2.PackageRevision, operationType string) OperationFinishFunc {
	return measureSourceOperation(ctx, pr, telemetry.Operations.Update, operationType)
}

// measureLifecycleUpdate times a lifecycle transition. It selects the
// PackageRevisionApproval resource in the case of a Published transition (conceptually an
// approval, if not strictly API-wise) and reports the settled lifecycle: desiredLifecycle
// on success, currentLifecycle on failure.
//
// The reported lifecycle is derived from the same *error the measurement uses to
// classify success/failure, so the two can never disagree.
//
// Usage (single line in the caller):
//
//	defer measureLifecycleUpdate(ctx, pr, currentLifecycle, desiredLifecycle)(&err)
func measureLifecycleUpdate(
	ctx context.Context,
	pr *v1alpha2.PackageRevision,
	currentLifecycle, desiredLifecycle v1alpha2.PackageRevisionLifecycle,
) OperationFinishFunc {
	name := prTelemetryName
	if v1alpha2.LifecycleIsPublished(pr.Spec.Lifecycle) {
		name = praTelemetryName
	}
	finish := measureControllerAPIOperation(
		ctx, telemetry.Operations.Update, name, pr.Spec.Lifecycle, pr)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha2.PackageRevisionLifecycle {
			if errPtr == nil || *errPtr == nil {
				return desiredLifecycle
			}
			return currentLifecycle
		},
		)
	}
}

// measurePkgRevResourcesUpdate times the operation to update a package revision's resources.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevResourcesUpdate(ctx, pr)(&err)
func measurePkgRevResourcesUpdate(ctx context.Context, pr *porchv1alpha2.PackageRevision) OperationFinishFunc {
	finish := measureControllerAPIOperation(
		ctx, telemetry.Operations.Update, prrTelemetryName, pr.Spec.Lifecycle, pr)
	return func(errPtr *error) { finish(errPtr, nil) }
}

// measurePkgRevDelete times the deletion of a package revision, including deletion from
// the upstream Git repository.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevDelete(ctx, pr)(&err)
func measurePkgRevDelete(ctx context.Context, pr *porchv1alpha2.PackageRevision) OperationFinishFunc {
	finish := measureControllerAPIOperation(
		ctx, telemetry.Operations.Delete, prTelemetryName, "", pr)
	return func(errPtr *error) {
		finish(errPtr, func() porchv1alpha2.PackageRevisionLifecycle {
			if errPtr != nil && *errPtr != nil {
				return pr.Spec.Lifecycle
			}
			return ""
		})
	}
}
