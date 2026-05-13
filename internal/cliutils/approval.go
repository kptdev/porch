// Copyright 2022 The kpt Authors
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

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ApproveErrorOut = "cannot change approval from %s to %s"

func UpdatePackageRevisionApproval(ctx context.Context, client client.Client, pr *porchapi.PackageRevision, new porchapi.PackageRevisionLifecycle) error {

	switch lifecycle := pr.Spec.Lifecycle; lifecycle {
	case porchapi.PackageRevisionLifecycleProposed:
		// Approve - change the package revision kind to 'final'.
		if new != porchapi.PackageRevisionLifecyclePublished && new != porchapi.PackageRevisionLifecycleDraft {
			return fmt.Errorf(ApproveErrorOut, lifecycle, new)
		}
	case porchapi.PackageRevisionLifecycleDeletionProposed:
		if new != porchapi.PackageRevisionLifecyclePublished {
			return fmt.Errorf(ApproveErrorOut, lifecycle, new)
		}
	case new:
		// already correct value
		return nil
	default:
		return fmt.Errorf(ApproveErrorOut, lifecycle, new)
	}

	pr.Spec.Lifecycle = new
	return client.SubResource("approval").Update(ctx, pr)
}
