// Copyright 2022, 2025 The kpt Authors
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

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/util"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// PackageRevisions Update Strategy

type packageRevisionStrategy struct{}

var _ SimpleRESTUpdateStrategy = packageRevisionStrategy{}

func (s packageRevisionStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newRevision := obj.(*porchapi.PackageRevision)
	oldRevision := old.(*porchapi.PackageRevision)

	//Defaults should be added here for newly added values in v1alpha1 to ensure forward compatiblity for server-side validations
	//Need to default in a non-empty value, because this field is discovered from Kptfile, not actually set on the API.
	if newRevision.Spec.PackageMetadata == nil {
		newRevision.Spec.PackageMetadata = oldRevision.Spec.PackageMetadata
	}
}

func (s packageRevisionStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	_, span := tracer.Start(ctx, "packageRevisionStrategy::ValidateUpdate", trace.WithAttributes())
	defer span.End()

	allErrs := field.ErrorList{}
	oldRevision := old.(*porchapi.PackageRevision)
	newRevision := obj.(*porchapi.PackageRevision)

	// Prevent users from modifying the kpt.dev/latest-revision label
	oldLatestRevision := ""
	newLatestRevision := ""
	if oldRevision.Labels != nil {
		oldLatestRevision = oldRevision.Labels[porchapi.LatestPackageRevisionKey]
	}
	if newRevision.Labels != nil {
		newLatestRevision = newRevision.Labels[porchapi.LatestPackageRevisionKey]
	}
	if oldLatestRevision != newLatestRevision {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("metadata", "labels", porchapi.LatestPackageRevisionKey), "label is managed by porch and cannot be modified by users"))
	}

	// Verify that the new lifecycle value is valid.
	switch lifecycle := newRevision.Spec.Lifecycle; lifecycle {
	case "", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:
		// valid
	default:
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle, fmt.Sprintf("value can be only updated to %s",
			strings.Join([]string{
				string(porchapi.PackageRevisionLifecycleDraft),
				string(porchapi.PackageRevisionLifecycleProposed),
				string(porchapi.PackageRevisionLifecyclePublished),
				string(porchapi.PackageRevisionLifecycleDeletionProposed),
			}, ",")),
		))
	}

	switch lifecycle := oldRevision.Spec.Lifecycle; lifecycle {
	case "", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed:
		// Packages in a draft or proposed state can only be updated to draft or proposed.
		newLifecycle := newRevision.Spec.Lifecycle
		if newLifecycle != porchapi.PackageRevisionLifecycleDraft &&
			newLifecycle != porchapi.PackageRevisionLifecycleProposed &&
			newLifecycle != "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle, fmt.Sprintf("value can be only updated to %s",
				strings.Join([]string{
					string(porchapi.PackageRevisionLifecycleDraft),
					string(porchapi.PackageRevisionLifecycleProposed),
				}, ",")),
			))
		}
	case porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:
		// We don't allow any updates to the spec for packagerevision that have been published. That includes updates of the lifecycle. But
		// we allow updates to metadata and status. The only exception is that the lifecycle
		// can change between Published and DeletionProposed and vice versa.
		newLifecycle := newRevision.Spec.Lifecycle
		if porchapi.LifecycleIsPublished(newLifecycle) {
			// copy the lifecycle value over before calling reflect.DeepEqual to allow comparison
			// of all other fields without error
			newRevision.Spec.Lifecycle = oldRevision.Spec.Lifecycle
		}
		if !equality.Semantic.DeepEqual(oldRevision.Spec, newRevision.Spec) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec"), newRevision.Spec, fmt.Sprintf("spec can only update package with lifecycle value one of %s",
				strings.Join([]string{
					string(porchapi.PackageRevisionLifecycleDraft),
					string(porchapi.PackageRevisionLifecycleProposed),
				}, ",")),
			))
		}
		newRevision.Spec.Lifecycle = newLifecycle
	default:
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle, fmt.Sprintf("can only update package with lifecycle value one of %s",
			strings.Join([]string{
				string(porchapi.PackageRevisionLifecycleDraft),
				string(porchapi.PackageRevisionLifecycleProposed),
				string(porchapi.PackageRevisionLifecyclePublished),
				string(porchapi.PackageRevisionLifecycleDeletionProposed),
			}, ",")),
		))
	}

	return allErrs
}

func (s packageRevisionStrategy) Canonicalize(obj runtime.Object) {
	pr := obj.(*porchapi.PackageRevision)
	if pr.Spec.Lifecycle == "" {
		// Set default
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDraft
	}
}

var _ SimpleRESTCreateStrategy = packageRevisionStrategy{}

// Validate returns an ErrorList with validation errors or nil.  Validate
// is invoked after default fields in the object have been filled in
// before the object is persisted.  This method should not mutate the
// object.
func (s packageRevisionStrategy) Validate(ctx context.Context, runtimeObj runtime.Object) field.ErrorList {
	_, span := tracer.Start(ctx, "packageRevisionStrategy::Validate", trace.WithAttributes())
	defer span.End()

	allErrs := field.ErrorList{}

	obj := runtimeObj.(*porchapi.PackageRevision)

	// Prevent users from setting the kpt.dev/latest-revision label during creation
	if obj.Labels != nil {
		if _, exists := obj.Labels[porchapi.LatestPackageRevisionKey]; exists {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("metadata", "labels", porchapi.LatestPackageRevisionKey), "label is managed by porch and cannot be set by users"))
		}
	}

	// A package name can have a path
	if pkgNameErr := util.ValidateDirectoryName(obj.Spec.PackageName, true); pkgNameErr != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "packageName"), obj.Spec.PackageName, pkgNameErr.Error()))
	}

	if wsNameErr := util.ValidateK8SName(string(obj.Spec.WorkspaceName)); wsNameErr != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "workspaceName"), obj.Spec.WorkspaceName, wsNameErr.Error()))
	}

	switch lifecycle := obj.Spec.Lifecycle; lifecycle {
	case "", porchapi.PackageRevisionLifecycleDraft:
		// valid

	default:
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "lifecycle"), lifecycle, fmt.Sprintf("value can be only created as %s",
			strings.Join([]string{
				string(porchapi.PackageRevisionLifecycleDraft),
			}, ",")),
		))
	}

	return allErrs
}

// Package Update Strategy

type packageStrategy struct{}

var _ SimpleRESTUpdateStrategy = packageStrategy{}

func (s packageStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (s packageStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return nil
}

func (s packageStrategy) Canonicalize(obj runtime.Object) {
}

var _ SimpleRESTCreateStrategy = packageStrategy{}

// Validate returns an ErrorList with validation errors or nil.  Validate
// is invoked after default fields in the object have been filled in
// before the object is persisted.  This method should not mutate the
// object.
func (s packageStrategy) Validate(ctx context.Context, runtimeObj runtime.Object) field.ErrorList {
	return nil
}
