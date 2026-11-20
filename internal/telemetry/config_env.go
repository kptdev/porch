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

package telemetry

import (
	"os"
	"strconv"

	configtypes "github.com/kptdev/porch/config/types"
)

// ApplyEnv copies telemetry settings into the process environment.
// SetupOpenTelemetry and the OpenTelemetry SDK read those variables themselves.
// Empty values are left unset so the existing defaults still apply.
func ApplyEnv(cfg configtypes.Telemetry) error {
	values := map[string]string{
		"OTEL_SERVICE_NAME":     cfg.ServiceName,
		"OTEL_METRICS_EXPORTER": cfg.MetricsExporter,
		"OTEL_TRACES_EXPORTER":  cfg.TracesExporter,
		otelHostEnv:             cfg.PrometheusHost,
	}
	if cfg.PrometheusPort != 0 {
		values[otelPortEnv] = strconv.Itoa(cfg.PrometheusPort)
	}
	if cfg.PprofPort != 0 {
		values[PProfPortEnvVar] = strconv.Itoa(cfg.PprofPort)
	} else if err := os.Unsetenv(PProfPortEnvVar); err != nil {
		return err
	}
	values[packageSizeDetailedEnv] = strconv.FormatBool(cfg.PackageSizeDetailed)
	for key, value := range values {
		if value == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}
