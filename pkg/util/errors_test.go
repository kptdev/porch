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
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/multierr"
)

func TestExtractImageFromError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedImage string
		expectedFound bool
	}{
		{
			name:          "found in simple",
			err:           &ExecutionError{Image: "image"},
			expectedImage: "image",
			expectedFound: true,
		},
		{
			name:          "found in wrapped",
			err:           &ExecutionError{Image: "image", Wrapped: errors.New("wrap")},
			expectedImage: "image",
			expectedFound: true,
		},
		{
			name:          "found in double wrapped",
			err:           errors.Wrap(&ExecutionError{Image: "image", Wrapped: errors.New("wrap")}, "out wrap"),
			expectedImage: "image",
			expectedFound: true,
		},
		{
			name:          "found in multierr and wrapped",
			err:           multierr.Append(errors.Wrap(&ExecutionError{Image: "image", Wrapped: errors.New("wrap")}, "left"), errors.New("right")),
			expectedImage: "image",
			expectedFound: true,
		},
		{
			name:          "missing",
			err:           multierr.Append(errors.Wrap(errors.New("wrap"), "left"), errors.New("right")),
			expectedImage: "",
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var exErr *ExecutionError
			errors.As(tt.err, &exErr)
			if tt.expectedFound {
				require.NotNil(t, exErr)
				require.Equal(t, tt.expectedImage, exErr.Image)
			} else {
				require.Nil(t, exErr)
			}
		})
	}
}
