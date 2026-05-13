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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Operation duration metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "porch_operation_duration_seconds",
			Help:    "Duration of Porch operations in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"operation", "repository", "package", "status"},
	)

	// Operation counter metrics
	operationCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "porch_operations_total",
			Help: "Total number of Porch operations",
		},
		[]string{"operation", "repository", "package", "status"},
	)

	// Repository metrics
	repositoryCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "porch_repositories_created_total",
			Help: "Total number of repositories created",
		},
	)

	// Package metrics
	packageCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "porch_packages_created_total",
			Help: "Total number of packages created",
		},
	)
)

// recordMetric records both duration and count for an operation
func recordMetric(operation, repoName, pkgName string, duration float64, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}

	operationDuration.WithLabelValues(operation, repoName, pkgName, status).Observe(duration)
	operationCounter.WithLabelValues(operation, repoName, pkgName, status).Inc()
}
