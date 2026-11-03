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

package lgcache

import (
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
)

func partitionRevisionKeys(left, right map[key.PackageRevisionKey]repository.PackageRevision) (leftOnly, both, rightOnly []key.PackageRevisionKey) {
	for k := range left {
		if _, ok := right[k]; ok {
			both = append(both, k)
		} else {
			leftOnly = append(leftOnly, k)
		}
	}
	for k := range right {
		if _, ok := left[k]; !ok {
			rightOnly = append(rightOnly, k)
		}
	}
	return leftOnly, both, rightOnly
}

func prMap(list []repository.PackageRevision) map[key.PackageRevisionKey]repository.PackageRevision {
	return repository.PrSlice2Map(list)
}

func commitTask(api *porchapi.PackageRevision) *porchapi.Task {
	if api == nil || len(api.Spec.Tasks) == 0 {
		return nil
	}
	task := api.Spec.Tasks[len(api.Spec.Tasks)-1]
	return &task
}

func upstreamRefName(tasks []porchapi.Task) string {
	for _, task := range tasks {
		switch task.Type {
		case porchapi.TaskTypeClone:
			if task.Clone != nil && task.Clone.Upstream.UpstreamRef != nil {
				return task.Clone.Upstream.UpstreamRef.Name
			}
		case porchapi.TaskTypeUpgrade:
			if task.Upgrade != nil {
				return task.Upgrade.NewUpstream.Name
			}
		}
	}
	return ""
}
