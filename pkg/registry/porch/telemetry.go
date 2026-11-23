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

package porch

import (
	"context"

	"github.com/kptdev/porch/api/porch"
	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/api/porch/v1alpha1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/kptdev/porch/pkg/repository"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
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

// measureServerAPIOperation starts timing and in-flight tracking for a single Porch API
// operation and returns a finish function, which must be deferred on the caller side to ensure
// the end of the operation is recorded.
//
// v1alpha1 specialisation of telemetry.MeasureAPIOperation(ctx, op, resource, apiVersion, desiredLifecycle, prKey) operationFinishFunc;
// automatically converts v1alpha1.PackageRevisionLifecycle to porch.PackageRevisionLifecycle.
//
//	defer telemetry.measureServerAPIOperation(ctx, op, resource, desiredLifecycle, prName)(&err)
//
// The op supplies both the verb (op.AllCaps) and, combined with resource, the
// porch operation name (op.TitleCase + resource). Pass a non-nil lifecycleAfter
// closure when the final recorded lifecycle differs from desiredLifecycle or must be
// computed from the final error.
func measureServerAPIOperation(ctx context.Context, op telemetry.OperationVerbs, resource string, desiredLifecycle v1alpha1.PackageRevisionLifecycle, prName string) func(errPtr *error, lifecycleAfter func() v1alpha1.PackageRevisionLifecycle) {
	namespace, _ := genericapirequest.NamespaceFrom(ctx)
	prKey := telemetry.ParsePkgRevK8sName2Key(namespace, prName)
	finish := telemetry.MeasureAPIOperation(ctx, op, resource, telemetry.APIVersionV1Alpha1, porch.PackageRevisionLifecycle(desiredLifecycle), &prKey)

	convertedLifecycleFinishFunc := func(errPtr *error, lifecycleAfter func() v1alpha1.PackageRevisionLifecycle) {
		var porchProvider func() porch.PackageRevisionLifecycle
		if lifecycleAfter != nil {
			porchProvider = func() porch.PackageRevisionLifecycle {
				return porch.PackageRevisionLifecycle(lifecycleAfter())
			}
		}
		finish(errPtr, porchProvider)
		telemetry.RecordServerRequestCount(ctx, resource, op.AllCaps)
	}
	return convertedLifecycleFinishFunc
}

// v1alpha1-specific analogue of telemetry.OperationFinishFunc
type OperationFinishFunc func(errPtr *error)

// measurePkgRevGet times getting a package revision.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevGet(ctx, prK8sName, apiPr, repoPr)(&err)
func measurePkgRevGet(ctx context.Context, prK8sName string, apiPr **v1alpha1.PackageRevision, repoPr *repository.PackageRevision) OperationFinishFunc {
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Get, prTelemetryName, v1alpha1.PackageRevisionLifecycle(""), prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return resolveLifecycleAfterOperation(ctx, *apiPr, *repoPr, err, "UNKNOWN")
		})
	}
}

// measurePkgRevResourcesGet times getting a package revision's resources.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevResourcesGet(ctx, prK8sName, repoPr)(&err)
func measurePkgRevResourcesGet(ctx context.Context, prK8sName string, repoPr *repository.PackageRevision) OperationFinishFunc {
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Get, prrTelemetryName, v1alpha1.PackageRevisionLifecycle(""), prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return resolveLifecycleAfterOperation(ctx, nil, *repoPr, err, "UNKNOWN")
		})
	}
}

// measurePkgRevApprovalGet times getting a package revision approval.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevApprovalGet(ctx, prK8sName, apiPr, repoPr)(&err)
func measurePkgRevApprovalGet(ctx context.Context, prK8sName string, apiPr **v1alpha1.PackageRevision, repoPr *repository.PackageRevision) OperationFinishFunc {
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Get, praTelemetryName, v1alpha1.PackageRevisionLifecycle(""), prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return resolveLifecycleAfterOperation(ctx, *apiPr, *repoPr, err, "UNKNOWN")
		})
	}
}

// measurePkgRevList times listing package revisions.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevList(ctx)(&err)
func measurePkgRevList(ctx context.Context) OperationFinishFunc {
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.List, prTelemetryName+"s", v1alpha1.PackageRevisionLifecycle(""), "")
	return func(errPtr *error) {
		finish(errPtr, nil)
	}
}

// measurePkgRevResourcesList times listing package revision resources.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevResourcesList(ctx)(&err)
func measurePkgRevResourcesList(ctx context.Context) OperationFinishFunc {
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.List, prrTelemetryName, v1alpha1.PackageRevisionLifecycle(""), "")
	return func(errPtr *error) {
		finish(errPtr, nil)
	}
}

// measurePkgRevCreate times the creation of a new package revision.
// The particular source action is recorded as part of the Porch operation
// (InitPackageRevision, ClonePackageRevision, EditPackageRevision, UpgradePackageRevision)
//
// Usage (single line in the caller):
//
//	defer measurePkgRevCreate(ctx, pr)(&err)
func measurePkgRevCreate(ctx context.Context, pr **v1alpha1.PackageRevision) OperationFinishFunc {
	pkgRevKey := key.PackageRevisionKey{}
	if *pr != nil {
		realPr := *pr
		pkgRevKey = key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Namespace: realPr.Namespace,
					Name:      realPr.Spec.RepositoryName,
				},
				Package: realPr.Spec.PackageName,
			},
			WorkspaceName: realPr.Spec.WorkspaceName}
	}

	op := telemetry.ParseOperation(createAction(*pr))
	op.AllCaps = telemetry.Operations.Create.AllCaps

	finish := measureServerAPIOperation(
		ctx, op, prTelemetryName, v1alpha1.PackageRevisionLifecycleDraft, pkgRevKey.K8SName())
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			if (errPtr != nil && *errPtr != nil) || *pr == nil {
				return ""
			}
			return (*pr).Spec.Lifecycle
		})
	}
}

// measurePkgRevUpdate times updating a package revision, including a lifecycle transition if applicable.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevUpdate(ctx, prK8sName, desiredUpdate, updatedApiPr, lifecycleAfter)(&err)
func measurePkgRevUpdate(ctx context.Context, prK8sName string, desiredUpdate rest.UpdatedObjectInfo, updatedApiPr **v1alpha1.PackageRevision, lifecycleAfter *v1alpha1.PackageRevisionLifecycle) OperationFinishFunc {
	desiredLifecycle := resolveDesiredLifecycleForUpdate(ctx, desiredUpdate, v1alpha1.PackageRevisionLifecycleDraft)
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Update, prTelemetryName, desiredLifecycle, prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return resolveLifecycleAfterOperation(ctx, *updatedApiPr, nil, err, *lifecycleAfter)
		})
	}
}

// measurePkgRevResourcesUpdate times updating a package revision, including a lifecycle transition if applicable.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevResourcesUpdate(ctx, prK8sName, repoPkgRev)(&err)
func measurePkgRevResourcesUpdate(ctx context.Context, prK8sName string, repoPkgRev *repository.PackageRevision) OperationFinishFunc {
	desiredLifecycle := resolveDesiredLifecycleForUpdate(ctx, nil, v1alpha1.PackageRevisionLifecycleDraft)
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Update, prrTelemetryName, desiredLifecycle, prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return resolveLifecycleAfterOperation(ctx, nil, *repoPkgRev, err, v1alpha1.PackageRevisionLifecycleDraft)
		})
	}
}

// measurePkgRevApprovalUpdate times updating a package revision's approval status
// (Proposed -> Published; Published -> DeletionProposed)
//
// Usage (single line in the caller):
//
//	defer measurePkgRevApprovalUpdate(ctx, prK8sName, desiredUpdate, updatedApiPr, lifecycleAfter)(&err)
func measurePkgRevApprovalUpdate(ctx context.Context, prK8sName string, desiredUpdate rest.UpdatedObjectInfo, updatedApiPr **v1alpha1.PackageRevision, lifecycleAfter *v1alpha1.PackageRevisionLifecycle) OperationFinishFunc {
	desiredLifecycle := resolveDesiredLifecycleForUpdate(ctx, desiredUpdate, v1alpha1.PackageRevisionLifecycleProposed)
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Update, praTelemetryName, desiredLifecycle, prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			var err error
			if errPtr != nil {
				err = *errPtr
			}
			return resolveLifecycleAfterOperation(ctx, *updatedApiPr, nil, err, *lifecycleAfter)
		})
	}
}

// measurePkgRevDelete times deleting a package revision.
//
// Usage (single line in the caller):
//
//	defer measurePkgRevDelete(ctx, prK8sName, apiPr, repoPr)(&err)
func measurePkgRevDelete(ctx context.Context, prK8sName string, apiPr **v1alpha1.PackageRevision, repoPr *repository.PackageRevision) OperationFinishFunc {
	finish := measureServerAPIOperation(
		ctx, telemetry.Operations.Delete, prTelemetryName, v1alpha1.PackageRevisionLifecycle(""), prK8sName)
	return func(errPtr *error) {
		finish(errPtr, func() v1alpha1.PackageRevisionLifecycle {
			if errPtr == nil || *errPtr == nil {
				// deleted package revision has no lifeycle
				return porchapi.PackageRevisionLifecycle("")
			}
			return resolveLifecycleAfterOperation(ctx, *apiPr, *repoPr, *errPtr, porchapi.PackageRevisionLifecycle("UNKNOWN"))
		})
	}
}
