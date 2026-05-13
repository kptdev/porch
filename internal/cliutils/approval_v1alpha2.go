// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UpdatePackageRevisionApprovalV1Alpha2 approves or rejects a v1alpha2 PackageRevision.
// CRDs don't support custom subresources, so this uses a regular Update.
func UpdatePackageRevisionApprovalV1Alpha2(ctx context.Context, c client.Client, pr *porchv1alpha2.PackageRevision, new porchv1alpha2.PackageRevisionLifecycle) error {
	switch lifecycle := pr.Spec.Lifecycle; lifecycle {
	case porchv1alpha2.PackageRevisionLifecycleProposed:
		if new != porchv1alpha2.PackageRevisionLifecyclePublished && new != porchv1alpha2.PackageRevisionLifecycleDraft {
			return fmt.Errorf(ApproveErrorOut, lifecycle, new)
		}
	case porchv1alpha2.PackageRevisionLifecycleDeletionProposed:
		if new != porchv1alpha2.PackageRevisionLifecyclePublished {
			return fmt.Errorf(ApproveErrorOut, lifecycle, new)
		}
	case new:
		return nil
	default:
		return fmt.Errorf(ApproveErrorOut, lifecycle, new)
	}

	pr.Spec.Lifecycle = new
	return c.Update(ctx, pr)
}
