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

package apiserver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestBuildCompleteScheme(t *testing.T) {
	scheme, err := buildCompleteScheme()
	require.NoError(t, err)
	require.NotNil(t, scheme)

	// Test singleton behavior - calling again should return same instance
	scheme2, err := buildCompleteScheme()
	require.NoError(t, err)
	assert.Same(t, scheme, scheme2, "expected buildCompleteScheme to return singleton instance")
}

func TestBuildSchemeWithTypes(t *testing.T) {
	tests := []struct {
		name        string
		builders    []schemeBuilder
		expectError bool
	}{
		{
			name: "success with valid builders",
			builders: []schemeBuilder{
				func(s *runtime.Scheme) error {
					return corev1.AddToScheme(s)
				},
			},
			expectError: false,
		},
		{
			name: "error from first builder",
			builders: []schemeBuilder{
				func(s *runtime.Scheme) error {
					return fmt.Errorf("mock error")
				},
			},
			expectError: true,
		},
		{
			name: "error from second builder",
			builders: []schemeBuilder{
				func(s *runtime.Scheme) error {
					return corev1.AddToScheme(s)
				},
				func(s *runtime.Scheme) error {
					return fmt.Errorf("second builder error")
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, err := buildSchemeWithTypes(tt.builders...)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "error")
				assert.Nil(t, scheme)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, scheme)
			}
		})
	}
}
