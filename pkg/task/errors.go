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

package task

import "fmt"

// RenderError indicates that the kpt render step itself failed.
// The draft may still contain valid (unrendered or partially-rendered) resources.
type RenderError struct {
	Err error
}

func (e *RenderError) Error() string { return e.Err.Error() }

// RenderPersistError indicates that rendering failed and the subsequent attempt
// to persist the (partially-rendered) resources to the draft also failed.
// Callers should NOT push the draft to the remote when this error is returned,
// because the draft contents are stale.
type RenderPersistError struct {
	RenderErr  error
	PersistErr error
}

func (e *RenderPersistError) Error() string {
	return fmt.Sprintf("failed to persist resources after render failure: %v: %v", e.PersistErr, e.RenderErr)
}
