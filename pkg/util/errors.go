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
	"fmt"
)

type ExecutionError struct {
	Image   string
	Wrapped error
}

func (e *ExecutionError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("func eval %q failed: %v", e.Image, e.Wrapped)
	}
	return fmt.Sprintf("func eval %q is failed", e.Image)
}

func (e *ExecutionError) Unwrap() error {
	return e.Wrapped
}

func (e *ExecutionError) WrappedMessage() string {
	if e.Wrapped != nil {
		return e.Wrapped.Error()
	}
	return "unknown reason"
}
