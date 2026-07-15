// Copyright 2026 The Nephio Authors
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
	"runtime"

	"github.com/grafana/pyroscope-go"
	"k8s.io/klog/v2"
)

const (
	PyroscopeServerEnvVar      = "PYROSCOPE_SERVER"
	PyroscopeAppNameEnvVar     = "PYROSCOPE_APP_NAME"
	PyroscopeAuthUserVar       = "PYROSCOPE_AUTH_USER"
	PyroscopeAuthPassVar       = "PYROSCOPE_AUTH_PASSWORD" // #nosec G101 -- env var name, not a credential (nolint:gosec)
	PyroscopeLogsEnabledEnvVar = "PYROSCOPE_LOGS_ENABLED"
)

const defaultPyroscopeAppName = "porch-server"

type PyroscopeProfiling struct {
	stop func() error
}

func (p *PyroscopeProfiling) Start() {
	serverURL := os.Getenv(PyroscopeServerEnvVar)
	if serverURL == "" {
		return
	}

	appName := os.Getenv(PyroscopeAppNameEnvVar)
	if appName == "" {
		appName = defaultPyroscopeAppName
	}

	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	var logger pyroscope.Logger
	logsEnabled := os.Getenv(PyroscopeLogsEnabledEnvVar)
	if logsEnabled == "true" || logsEnabled == "1" {
		logger = pyroscope.StandardLogger
	}

	cfg := pyroscope.Config{
		ApplicationName: appName,
		ServerAddress:   serverURL,
		Logger:          logger,
		Tags: map[string]string{
			"service_name": appName,
		},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	}
	if user := os.Getenv(PyroscopeAuthUserVar); user != "" {
		cfg.BasicAuthUser = user
	}
	if password := os.Getenv(PyroscopeAuthPassVar); password != "" {
		cfg.BasicAuthPassword = password
	}

	profiler, err := pyroscope.Start(cfg)
	if err != nil {
		klog.Warningf("Failed to start Pyroscope profiling: %v", err)
		return
	}
	p.stop = profiler.Stop
	klog.Infof("Pyroscope continuous profiling started (server=%q, app=%q)", serverURL, appName)
}

func (p *PyroscopeProfiling) Stop() {
	if p.stop != nil {
		klog.Infof("Stopping Pyroscope profiler")
		if err := p.stop(); err != nil {
			klog.Warningf("Pyroscope profiler stop: %v", err)
		}
		p.stop = nil
	}
}
