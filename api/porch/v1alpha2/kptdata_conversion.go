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

package v1alpha2

import (
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
)

// KptfileToPackageConditions converts Kptfile status conditions to v1alpha2 PackageConditions.
func KptfileToPackageConditions(kf kptfilev1.KptFile) []PackageCondition {
	if kf.Status == nil {
		return nil
	}
	conds := make([]PackageCondition, 0, len(kf.Status.Conditions))
	for _, c := range kf.Status.Conditions {
		conds = append(conds, PackageCondition{
			Type:    c.Type,
			Status:  PackageConditionStatus(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	return conds
}

// KptfileToReadinessGates converts Kptfile readiness gates to v1alpha2 ReadinessGates.
func KptfileToReadinessGates(kf kptfilev1.KptFile) []ReadinessGate {
	if kf.Info == nil {
		return nil
	}
	gates := make([]ReadinessGate, 0, len(kf.Info.ReadinessGates))
	for _, rg := range kf.Info.ReadinessGates {
		gates = append(gates, ReadinessGate{ConditionType: rg.ConditionType})
	}
	return gates
}

// KptfileToPackageMetadata converts Kptfile labels and annotations to v1alpha2 PackageMetadata.
func KptfileToPackageMetadata(kf kptfilev1.KptFile) *PackageMetadata {
	if len(kf.Labels) == 0 && len(kf.Annotations) == 0 {
		return nil
	}
	return &PackageMetadata{
		Labels:      kf.Labels,
		Annotations: kf.Annotations,
	}
}

// KptLocatorToLocator converts a kpt Locator to a v1alpha2 Locator.
// The two types are structurally identical but live in different packages.
func KptLocatorToLocator(lock kptfilev1.Locator) *Locator {
	if lock.Git == nil {
		return nil
	}
	return &Locator{
		Type: OriginType(lock.Type),
		Git: &GitLock{
			Repo:      lock.Git.Repo,
			Directory: lock.Git.Directory,
			Ref:       lock.Git.Ref,
			Commit:    lock.Git.Commit,
		},
	}
}
