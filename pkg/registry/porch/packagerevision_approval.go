// Copyright 2022 The kpt Authors
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
	"fmt"
	"strings"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	pctx "github.com/kptdev/porch/pkg/util/context"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

const praTelemetryName = "PackageRevisionApproval"

type packageRevisionApproval struct {
	packageCommon
}

var _ rest.Storage = &packageRevisionApproval{}
var _ rest.Scoper = &packageRevisionApproval{}
var _ rest.Getter = &packageRevisionApproval{}
var _ rest.Updater = &packageRevisionApproval{}

// New returns an empty object that can be used with Create and Update after request data has been put into it.
// This object must be a pointer type for use with Codec.DecodeInto([]byte, runtime.Object)
func (a *packageRevisionApproval) New() runtime.Object {
	return &porchapi.PackageRevision{}
}

func (a *packageRevisionApproval) Destroy() {}

// NamespaceScoped returns true if the storage is namespaced
func (a *packageRevisionApproval) NamespaceScoped() bool {
	return true
}

func (a *packageRevisionApproval) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisionApproval::Get", trace.WithAttributes())
	start := time.Now()
	defer func() {
		span.End()
		telemetry.RecordAPICallDuration(praTelemetryName, "GET", time.Since(start).Seconds())
	}()

	telemetry.RecordRequestCount(ctx, prTelemetryName, "GET")

	ctx = pctx.WithNewRequestIDAndPackageRevision(ctx, name)

	pkg, err := a.getRepoPkgRev(ctx, name)
	if err != nil {
		return nil, err
	}
	return pkg.GetPackageRevision(ctx)
}

// Update finds a resource in the storage and updates it. Some implementations
// may allow updates creates the object - they should set the created boolean
// to true.
func (a *packageRevisionApproval) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc, _ bool, _ *metav1.UpdateOptions) (runtime.Object, bool, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisionApproval::Update", trace.WithAttributes())
	start := time.Now()
	defer func() {
		span.End()
		telemetry.RecordAPICallDuration(praTelemetryName, "UPDATE", time.Since(start).Seconds())
	}()

	telemetry.RecordRequestCount(ctx, prTelemetryName, "GET")

	ctx = pctx.WithNewRequestIDAndPackageRevision(ctx, name)

	allowCreate := false // do not allow create on update
	runTimeObj, ok, err := a.updatePackageRevision(ctx, name, objInfo, createValidation, updateValidation, allowCreate)
	if err != nil {
		klog.ErrorS(err, "[API] PackageRevision approval operation failed", pctx.LogMetadataFrom(ctx)...)
	}
	return runTimeObj, ok, err
}

type packageRevisionApprovalStrategy struct{}

func (s packageRevisionApprovalStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (s packageRevisionApprovalStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	allErrs := field.ErrorList{}
	oldRevision := old.(*porchapi.PackageRevision)
	newRevision := obj.(*porchapi.PackageRevision)

	switch lifecycle := oldRevision.Spec.Lifecycle; lifecycle {

	case porchapi.PackageRevisionLifecyclePublished:
		if newRevision.Spec.Lifecycle != porchapi.PackageRevisionLifecycleDeletionProposed {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle,
				fmt.Sprintf("package with %s lifecycle value can only be updated to 'ProposeDeletion'", lifecycle)))
		}

	case porchapi.PackageRevisionLifecycleDeletionProposed:
		if newRevision.Spec.Lifecycle != porchapi.PackageRevisionLifecyclePublished {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle,
				fmt.Sprintf("package with %s lifecycle value can only be updated to 'Published'", lifecycle)))
		}

	case porchapi.PackageRevisionLifecycleProposed:
		// valid

	default:
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle,
			fmt.Sprintf("cannot approve package with %s lifecycle value; only Proposed packages can be approved", lifecycle)))
	}

	switch lifecycle := newRevision.Spec.Lifecycle; lifecycle {
	// TODO: signal rejection of the approval differently than by returning to draft?
	case porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecyclePublished:
		// valid

	case porchapi.PackageRevisionLifecycleDeletionProposed:
		if oldRevision.Spec.Lifecycle != porchapi.PackageRevisionLifecyclePublished {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle,
				fmt.Sprintf("cannot update lifecycle %s; only Published packages require approval for deletion", lifecycle)))
		}

	default:
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle, fmt.Sprintf("value for approval can be only one of %s",
				strings.Join([]string{
					string(porchapi.PackageRevisionLifecycleDraft),
					string(porchapi.PackageRevisionLifecyclePublished),
				}, ",")),
			))
	}
	return allErrs
}

func (s packageRevisionApprovalStrategy) Canonicalize(obj runtime.Object) {}

var _ SimpleRESTCreateStrategy = packageRevisionApprovalStrategy{}

// Validate returns an ErrorList with validation errors or nil.  Validate
// is invoked after default fields in the object have been filled in
// before the object is persisted.  This method should not mutate the
// object.
func (s packageRevisionApprovalStrategy) Validate(ctx context.Context, runtimeObj runtime.Object) field.ErrorList {
	allErrs := field.ErrorList{}

	// obj := runtimeObj.(*api.PackageRevision)

	return allErrs
}
