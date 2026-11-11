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

package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransitionAllowed_directPublishDisabled(t *testing.T) {
	opts := Options{AllowDirectPublicationFromDraft: false}
	assert.True(t, TransitionAllowed(opts, StageDraft, StageProposed))
	assert.False(t, TransitionAllowed(opts, StageDraft, StagePublished))
	assert.True(t, TransitionAllowed(opts, StageProposed, StagePublished))
}

func TestTransitionAllowed_directPublishEnabled(t *testing.T) {
	opts := Options{AllowDirectPublicationFromDraft: true}
	assert.True(t, TransitionAllowed(opts, StageDraft, StagePublished))
}

func TestValidateTransition_directPublishDisabled(t *testing.T) {
	opts := Options{AllowDirectPublicationFromDraft: false}
	err := ValidateTransition(opts, StageDraft, StagePublished)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "direct publication from Draft is disabled")
}

func TestNextTransitionToward_coalescedProposePublish(t *testing.T) {
	opts := Options{AllowDirectPublicationFromDraft: false}
	next, err := NextTransitionToward(opts, StageDraft, StagePublished)
	require.NoError(t, err)
	assert.Equal(t, StageProposed, next)
}

func TestNextTransitionToward_directPublishEnabled(t *testing.T) {
	opts := Options{AllowDirectPublicationFromDraft: true}
	next, err := NextTransitionToward(opts, StageDraft, StagePublished)
	require.NoError(t, err)
	assert.Equal(t, StagePublished, next)
}
