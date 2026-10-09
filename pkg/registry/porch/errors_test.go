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

package porch

import (
	"errors"
	"fmt"
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestWrapIfNotApiErrorPreservesConflict(t *testing.T) {
	// given
	conflict := apierrors.NewConflict(
		porchapi.Resource("packagerevisions"),
		"test-pr",
		fmt.Errorf("the object has been modified; please apply your changes to the latest version and try again"),
	)

	// when
	err := WrapIfNotApiError(conflict)

	// then
	require.True(t, apierrors.IsConflict(err))
	require.ErrorContains(t, err, "the object has been modified")
}

func TestWrapIfNotApiErrorPreservesNotAcceptable(t *testing.T) {
	// given
	notAcceptable := newResourceNotAcceptableError(t.Context(), porchapi.Resource("packagerevisions"))

	// when
	err := WrapIfNotApiError(notAcceptable)

	// then
	require.Equal(t, notAcceptable, err)
}

func TestWrapIfNotApiErrorWrapsGenericError(t *testing.T) {
	// given
	genericErr := errors.New("engine failed")

	// when
	err := WrapIfNotApiError(genericErr)

	// then
	require.True(t, apierrors.IsInternalError(err))
	require.ErrorContains(t, err, "engine failed")
}

func TestWrapIfNotApiErrorReturnsNil(t *testing.T) {
	// given / when
	err := WrapIfNotApiError(nil)

	// then
	require.NoError(t, err)
}
