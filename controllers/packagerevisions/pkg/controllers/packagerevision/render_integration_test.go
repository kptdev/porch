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

package packagerevision

import (
	"testing"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

// RenderIntegrationTestSuite tests render logic and annotation handling.
// PRRStore database integration is covered in pkg/prrstore/sql_test.go.
type RenderIntegrationTestSuite struct {
	suite.Suite
}

func TestRenderIntegrationTestSuite(t *testing.T) {
	suite.Run(t, &RenderIntegrationTestSuite{})
}

// TestAnnotationTrackingAfterRender verifies annotation synchronization logic.
func (s *RenderIntegrationTestSuite) TestAnnotationTrackingAfterRender() {
	// Create a mock PackageRevision with render request annotation
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	// Set render-request annotation
	pr.Annotations = map[string]string{
		porchv1alpha2.AnnotationRenderRequest: "v1",
	}

	// Verify the annotation is present
	requested := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
	s.Equal("v1", requested, "render-request annotation should be set")

	// After render completes, ObservedPrrResourceVersion should match annotation
	observedVersion := observedVersionAfterRender(requested, pr.Annotations)
	s.Equal("v1", observedVersion, "observed version should match requested")
}

// TestRenderTriggerDetection verifies render trigger detection logic.
func (s *RenderIntegrationTestSuite) TestRenderTriggerDetection() {
	tests := []struct {
		name          string
		lifecycle     porchv1alpha2.PackageRevisionLifecycle
		hasAnnotation bool
		hasSource     bool
		isRendered    bool
		expectAnnot   bool
		expectSource  bool
	}{
		{
			name:         "not draft - no trigger",
			lifecycle:    porchv1alpha2.PackageRevisionLifecyclePublished,
			expectAnnot:  false,
			expectSource: false,
		},
		{
			name:          "draft with annotation - trigger",
			lifecycle:     porchv1alpha2.PackageRevisionLifecycleDraft,
			hasAnnotation: true,
			isRendered:    true,
			expectAnnot:   true,
			expectSource:  false,
		},
		{
			name:         "draft with source, not rendered - trigger",
			lifecycle:    porchv1alpha2.PackageRevisionLifecycleDraft,
			hasSource:    true,
			isRendered:   false,
			expectAnnot:  false,
			expectSource: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			pr := &porchv1alpha2.PackageRevision{
				Spec: porchv1alpha2.PackageRevisionSpec{
					Lifecycle: tt.lifecycle,
				},
				Status: porchv1alpha2.PackageRevisionStatus{},
			}

			if tt.hasAnnotation {
				pr.Annotations = map[string]string{
					porchv1alpha2.AnnotationRenderRequest: "v1",
				}
			}

			if tt.hasSource {
				pr.Status.CreationSource = "clone"
			}

			if tt.isRendered {
				pr.Status.Conditions = []v1.Condition{
					{
						Type:   porchv1alpha2.ConditionRendered,
						Status: v1.ConditionTrue,
					},
				}
			}

			_, annot, source := renderTrigger(pr)
			s.Equal(tt.expectAnnot, annot, "annotation trigger mismatch")
			s.Equal(tt.expectSource, source, "source trigger mismatch")
		})
	}
}

// TestRenderStaleness verifies stale render detection.
func (s *RenderIntegrationTestSuite) TestRenderStaleness() {
	tests := []struct {
		name         string
		currentAnnot string
		rendered     string
		expectStale  bool
	}{
		{
			name:         "same - not stale",
			currentAnnot: "v1",
			rendered:     "v1",
			expectStale:  false,
		},
		{
			name:         "different - stale",
			currentAnnot: "v2",
			rendered:     "v1",
			expectStale:  true,
		},
		{
			name:         "empty current, rendered set - stale",
			currentAnnot: "",
			rendered:     "v1",
			expectStale:  true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			stale := isRenderStale(tt.currentAnnot, tt.rendered)
			s.Equal(tt.expectStale, stale, "stale detection mismatch")
		})
	}
}

// TestObservedVersionAfterRender verifies the observed version calculation.
func (s *RenderIntegrationTestSuite) TestObservedVersionAfterRender() {
	tests := []struct {
		name        string
		requested   string
		annotations map[string]string
		expected    string
	}{
		{
			name:      "requested version provided",
			requested: "user-provided",
			annotations: map[string]string{
				porchv1alpha2.AnnotationRenderRequest: "v1",
			},
			expected: "user-provided",
		},
		{
			name:      "fallback to annotation",
			requested: "",
			annotations: map[string]string{
				porchv1alpha2.AnnotationRenderRequest: "v2",
			},
			expected: "v2",
		},
		{
			name:        "no request, no annotation",
			requested:   "",
			annotations: map[string]string{},
			expected:    "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := observedVersionAfterRender(tt.requested, tt.annotations)
			s.Equal(tt.expected, result, "observed version mismatch")
		})
	}
}

// TestKptfileFromResources verifies Kptfile parsing from resource map.
func (s *RenderIntegrationTestSuite) TestKptfileFromResources() {
	tests := []struct {
		name        string
		resources   map[string]string
		expectError bool
		expectEmpty bool
	}{
		{
			name: "valid Kptfile",
			resources: map[string]string{
				"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: test\n",
			},
			expectError: false,
			expectEmpty: false,
		},
		{
			name:        "missing Kptfile",
			resources:   map[string]string{},
			expectError: false,
			expectEmpty: true,
		},
		{
			name: "empty Kptfile",
			resources: map[string]string{
				"Kptfile": "",
			},
			expectError: false,
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			kf, err := kptfileFromResources(tt.resources)
			if tt.expectError {
				s.Error(err)
			} else {
				s.NoError(err)
				if tt.expectEmpty {
					s.Empty(kf.Kind)
				} else {
					s.NotEmpty(kf.Kind)
				}
			}
		})
	}
}

// TestIsRenderedTrue verifies rendered condition checking.
func (s *RenderIntegrationTestSuite) TestIsRenderedTrue() {
	tests := []struct {
		name       string
		conditions []v1.Condition
		expectTrue bool
	}{
		{
			name: "rendered condition true",
			conditions: []v1.Condition{
				{
					Type:   porchv1alpha2.ConditionRendered,
					Status: v1.ConditionTrue,
				},
			},
			expectTrue: true,
		},
		{
			name: "rendered condition false",
			conditions: []v1.Condition{
				{
					Type:   porchv1alpha2.ConditionRendered,
					Status: v1.ConditionFalse,
				},
			},
			expectTrue: false,
		},
		{
			name:       "no conditions",
			conditions: []v1.Condition{},
			expectTrue: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			pr := &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					Conditions: tt.conditions,
				},
			}
			result := isRenderedTrue(pr)
			s.Equal(tt.expectTrue, result)
		})
	}
}

// TestIsPushOnRenderFailure verifies push-on-render-failure flag detection.
func (s *RenderIntegrationTestSuite) TestIsPushOnRenderFailure() {
	tests := []struct {
		name        string
		annotations map[string]string
		expectTrue  bool
	}{
		{
			name: "push-on-render-failure enabled",
			annotations: map[string]string{
				porchv1alpha2.PushOnFnRenderFailureKey: porchv1alpha2.PushOnFnRenderFailureValue,
			},
			expectTrue: true,
		},
		{
			name:        "no annotations",
			annotations: map[string]string{},
			expectTrue:  false,
		},
		{
			name: "annotation with wrong value",
			annotations: map[string]string{
				porchv1alpha2.PushOnFnRenderFailureKey: "false",
			},
			expectTrue: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			pr := &porchv1alpha2.PackageRevision{
				ObjectMeta: v1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			result := isPushOnRenderFailure(pr)
			s.Equal(tt.expectTrue, result)
		})
	}
}
