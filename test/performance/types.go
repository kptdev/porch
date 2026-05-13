// Copyright 2024, 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package metrics

import (
	"time"
)

// OperationMetrics holds metrics for a single operation
type OperationMetrics struct {
	Operation string
	Duration  time.Duration
	Error     error
}

// TestMetrics holds metrics for a test iteration
type TestMetrics struct {
	RepoName string
	PkgName  string
	Metrics  []OperationMetrics
}

// Stats holds statistics for operations
type Stats struct {
	Min   time.Duration
	Max   time.Duration
	Total time.Duration
	Count int
}
