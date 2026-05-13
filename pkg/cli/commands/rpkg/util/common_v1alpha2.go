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

package util

import (
	"context"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func PackageAlreadyExistsV1Alpha2(ctx context.Context, c client.Client, repository, packageName, namespace string) (bool, error) {
	var list porchv1alpha2.PackageRevisionList
	if err := c.List(ctx, &list, &client.ListOptions{Namespace: namespace}); err != nil {
		return false, err
	}
	for _, pr := range list.Items {
		if pr.Spec.RepositoryName == repository && pr.Spec.PackageName == packageName {
			return true, nil
		}
	}
	return false, nil
}
