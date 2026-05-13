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

	"github.com/stretchr/testify/assert"
)

func TestLifecycleIsPublished(t *testing.T) {
	tests := []struct {
		lifecycle PackageRevisionLifecycle
		expected  bool
	}{
		{PackageRevisionLifecyclePublished, true},
		{PackageRevisionLifecycleDeletionProposed, true},
		{PackageRevisionLifecycleDraft, false},
		{PackageRevisionLifecycleProposed, false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.lifecycle), func(t *testing.T) {
			assert.Equal(t, tt.expected, LifecycleIsPublished(tt.lifecycle))
		})
	}
}

func TestIsPublished(t *testing.T) {
	published := &PackageRevision{Spec: PackageRevisionSpec{Lifecycle: PackageRevisionLifecyclePublished}}
	draft := &PackageRevision{Spec: PackageRevisionSpec{Lifecycle: PackageRevisionLifecycleDraft}}

	assert.True(t, published.IsPublished())
	assert.False(t, draft.IsPublished())
}

func TestPackageRevisionIsReady(t *testing.T) {
	tests := []struct {
		name       string
		gates      []ReadinessGate
		conditions []PackageCondition
		expected   bool
	}{
		{
			name:     "no gates - always ready",
			expected: true,
		},
		{
			name:  "gate met",
			gates: []ReadinessGate{{ConditionType: "Ready"}},
			conditions: []PackageCondition{
				{Type: "Ready", Status: PackageConditionTrue},
			},
			expected: true,
		},
		{
			name:       "gate missing condition",
			gates:      []ReadinessGate{{ConditionType: "Ready"}},
			conditions: nil,
			expected:   false,
		},
		{
			name:  "gate condition not true",
			gates: []ReadinessGate{{ConditionType: "Ready"}},
			conditions: []PackageCondition{
				{Type: "Ready", Status: PackageConditionFalse},
			},
			expected: false,
		},
		{
			name: "multiple gates - one unmet",
			gates: []ReadinessGate{
				{ConditionType: "Ready"},
				{ConditionType: "Validated"},
			},
			conditions: []PackageCondition{
				{Type: "Ready", Status: PackageConditionTrue},
			},
			expected: false,
		},
		{
			name: "multiple gates - all met",
			gates: []ReadinessGate{
				{ConditionType: "Ready"},
				{ConditionType: "Validated"},
			},
			conditions: []PackageCondition{
				{Type: "Ready", Status: PackageConditionTrue},
				{Type: "Validated", Status: PackageConditionTrue},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, PackageRevisionIsReady(tt.gates, tt.conditions))
		})
	}
}

func TestIsPackageCreation(t *testing.T) {
	tests := []struct {
		name     string
		source   *PackageSource
		expected bool
	}{
		{name: "nil source", source: nil, expected: false},
		{name: "init", source: &PackageSource{Init: &PackageInitSpec{}}, expected: true},
		{name: "clone", source: &PackageSource{CloneFrom: &UpstreamPackage{}}, expected: true},
		{name: "copy", source: &PackageSource{CopyFrom: &PackageRevisionRef{}}, expected: false},
		{name: "upgrade", source: &PackageSource{Upgrade: &PackageUpgradeSpec{}}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &PackageRevision{Spec: PackageRevisionSpec{Source: tt.source}}
			assert.Equal(t, tt.expected, IsPackageCreation(pr))
		})
	}
}
