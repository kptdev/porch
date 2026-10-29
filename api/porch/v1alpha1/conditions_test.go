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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPackageRevisionStatusConditions(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: RenderFinishedConditionType, Status: metav1.ConditionTrue},
		{Type: RenderedConditionType, Status: metav1.ConditionFalse},
	}

	assert.True(t, meta.IsStatusConditionTrue(conditions, RenderFinishedConditionType))
	assert.False(t, meta.IsStatusConditionTrue(conditions, RenderedConditionType))
	assert.True(t, meta.IsStatusConditionFalse(conditions, RenderedConditionType))

	found := meta.FindStatusCondition(conditions, RenderFinishedConditionType)
	assert.NotNil(t, found)
	assert.Equal(t, metav1.ConditionTrue, found.Status)
	assert.Nil(t, meta.FindStatusCondition(conditions, "Missing"))
}

func TestLifecycleIsDraft(t *testing.T) {
	assert.True(t, LifecycleIsDraft(PackageRevisionLifecycleDraft))
	assert.True(t, LifecycleIsDraft(PackageRevisionLifecycleProposed))
	assert.False(t, LifecycleIsDraft(PackageRevisionLifecyclePublished))
}
