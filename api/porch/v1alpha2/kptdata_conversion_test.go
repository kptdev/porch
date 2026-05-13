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
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/stretchr/testify/assert"
)

func TestKptfileToPackageConditions(t *testing.T) {
	// nil status
	assert.Nil(t, KptfileToPackageConditions(kptfilev1.KptFile{}))

	// empty conditions
	kf := kptfilev1.KptFile{Status: &kptfilev1.Status{}}
	assert.Empty(t, KptfileToPackageConditions(kf))

	// populated conditions
	kf.Status.Conditions = []kptfilev1.Condition{
		{Type: "Ready", Status: kptfilev1.ConditionTrue, Reason: "AllGood", Message: "all good"},
		{Type: "Validated", Status: kptfilev1.ConditionFalse, Reason: "Failed", Message: "bad input"},
	}
	conds := KptfileToPackageConditions(kf)
	assert.Len(t, conds, 2)
	assert.Equal(t, "Ready", conds[0].Type)
	assert.Equal(t, PackageConditionTrue, conds[0].Status)
	assert.Equal(t, "AllGood", conds[0].Reason)
	assert.Equal(t, "all good", conds[0].Message)
	assert.Equal(t, "Validated", conds[1].Type)
	assert.Equal(t, PackageConditionFalse, conds[1].Status)
}

func TestKptfileToReadinessGates(t *testing.T) {
	// nil info
	assert.Nil(t, KptfileToReadinessGates(kptfilev1.KptFile{}))

	// empty gates
	kf := kptfilev1.KptFile{Info: &kptfilev1.PackageInfo{}}
	assert.Empty(t, KptfileToReadinessGates(kf))

	// populated gates
	kf.Info.ReadinessGates = []kptfilev1.ReadinessGate{
		{ConditionType: "Ready"},
		{ConditionType: "Validated"},
	}
	gates := KptfileToReadinessGates(kf)
	assert.Len(t, gates, 2)
	assert.Equal(t, "Ready", gates[0].ConditionType)
	assert.Equal(t, "Validated", gates[1].ConditionType)
}

func TestKptfileToPackageMetadata(t *testing.T) {
	// no labels or annotations
	assert.Nil(t, KptfileToPackageMetadata(kptfilev1.KptFile{}))

	// labels only
	kf := kptfilev1.KptFile{}
	kf.Labels = map[string]string{"app": "foo"}
	meta := KptfileToPackageMetadata(kf)
	assert.Equal(t, "foo", meta.Labels["app"])
	assert.Nil(t, meta.Annotations)

	// annotations only
	kf = kptfilev1.KptFile{}
	kf.Annotations = map[string]string{"note": "bar"}
	meta = KptfileToPackageMetadata(kf)
	assert.Nil(t, meta.Labels)
	assert.Equal(t, "bar", meta.Annotations["note"])

	// both
	kf = kptfilev1.KptFile{}
	kf.Labels = map[string]string{"app": "foo"}
	kf.Annotations = map[string]string{"note": "bar"}
	meta = KptfileToPackageMetadata(kf)
	assert.Equal(t, "foo", meta.Labels["app"])
	assert.Equal(t, "bar", meta.Annotations["note"])
}

func TestKptLocatorToLocator(t *testing.T) {
	// nil git
	assert.Nil(t, KptLocatorToLocator(kptfilev1.Locator{}))

	// populated
	lock := kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git: &kptfilev1.GitLock{
			Repo:      "https://github.com/example/repo.git",
			Directory: "pkg/foo",
			Ref:       "v1.0.0",
			Commit:    "abc123",
		},
	}
	loc := KptLocatorToLocator(lock)
	assert.Equal(t, OriginType(kptfilev1.GitOrigin), loc.Type)
	assert.Equal(t, "https://github.com/example/repo.git", loc.Git.Repo)
	assert.Equal(t, "pkg/foo", loc.Git.Directory)
	assert.Equal(t, "v1.0.0", loc.Git.Ref)
	assert.Equal(t, "abc123", loc.Git.Commit)
}
