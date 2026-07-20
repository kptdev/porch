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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPyroscopeProfiling_Start_NoServer(t *testing.T) {
	t.Setenv(PyroscopeServerEnvVar, "")

	var p PyroscopeProfiling
	p.Start()

	assert.Nil(t, p.stop)
}

func TestPyroscopeProfiling_Start_InvalidServer(t *testing.T) {
	t.Setenv(PyroscopeServerEnvVar, "://invalid-url")

	var p PyroscopeProfiling
	p.Start()

	assert.Nil(t, p.stop)
}

func TestPyroscopeProfiling_Start_Success_DefaultAppName(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv(PyroscopeServerEnvVar, ts.URL)
	t.Setenv(PyroscopeAppNameEnvVar, "")
	t.Setenv(PyroscopeAuthUserVar, "")
	t.Setenv(PyroscopeAuthPassVar, "")
	t.Setenv(PyroscopeLogsEnabledEnvVar, "")

	var p PyroscopeProfiling
	p.Start()
	require.NotNil(t, p.stop)
	t.Cleanup(func() { p.Stop() })
}

func TestPyroscopeProfiling_Start_Success_CustomConfig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv(PyroscopeServerEnvVar, ts.URL)
	t.Setenv(PyroscopeAppNameEnvVar, "custom-app")
	t.Setenv(PyroscopeAuthUserVar, "user")
	t.Setenv(PyroscopeAuthPassVar, "pass")
	t.Setenv(PyroscopeLogsEnabledEnvVar, "1")

	var p PyroscopeProfiling
	p.Start()
	require.NotNil(t, p.stop)
	p.Stop()
	assert.Nil(t, p.stop)
}

func TestPyroscopeProfiling_Start_LogsEnabledTrue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv(PyroscopeServerEnvVar, ts.URL)
	t.Setenv(PyroscopeAppNameEnvVar, "logs-app")
	t.Setenv(PyroscopeLogsEnabledEnvVar, "true")

	var p PyroscopeProfiling
	p.Start()
	require.NotNil(t, p.stop)
	p.Stop()
}

func TestPyroscopeProfiling_Stop_NoProfiler(t *testing.T) {
	var p PyroscopeProfiling
	assert.NotPanics(t, func() { p.Stop() })
}

func TestPyroscopeProfiling_Stop_WithError(t *testing.T) {
	var p PyroscopeProfiling
	p.stop = func() error {
		return errors.New("stop failed")
	}

	assert.NotPanics(t, func() { p.Stop() })
	assert.Nil(t, p.stop)
}

func TestPyroscopeProfiling_Stop_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv(PyroscopeServerEnvVar, ts.URL)
	t.Setenv(PyroscopeAppNameEnvVar, fmt.Sprintf("stop-test-%d", time.Now().UnixNano()))

	var p PyroscopeProfiling
	p.Start()
	require.NotNil(t, p.stop)
	assert.NotPanics(t, func() { p.Stop() })
	assert.Nil(t, p.stop)
}
