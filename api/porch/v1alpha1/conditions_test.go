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

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPackageRevisionStatusConditions(t *testing.T) {
	pr := &PackageRevision{
		Status: PackageRevisionStatus{
			Conditions: []Condition{
				{Type: RenderFinishedConditionType, Status: ConditionTrue},
				{Type: RenderedConditionType, Status: ConditionFalse},
			},
		},
	}

	assert.True(t, pr.IsStatusConditionTrue(RenderFinishedConditionType))
	assert.False(t, pr.IsStatusConditionTrue(RenderedConditionType))
	assert.True(t, pr.IsStatusConditionFalse(RenderedConditionType))

	found := pr.FindStatusCondition(RenderFinishedConditionType)
	assert.NotNil(t, found)
	assert.Equal(t, ConditionTrue, found.Status)
	assert.Nil(t, pr.FindStatusCondition("Missing"))
}

func TestLifecycleIsDraft(t *testing.T) {
	assert.True(t, LifecycleIsDraft(PackageRevisionLifecycleDraft))
	assert.True(t, LifecycleIsDraft(PackageRevisionLifecycleProposed))
	assert.False(t, LifecycleIsDraft(PackageRevisionLifecyclePublished))
}

func TestRevisionHelpers(t *testing.T) {
	assert.Equal(t, 3, Revision2Int("v3"))
	assert.Equal(t, int64(3), Revision2Int64("v3"))
	assert.Equal(t, "3", Revision2Str(3))
}
