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
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfiling_Start_NoEnv(t *testing.T) {
	t.Setenv(PProfPortEnvVar, "")

	var p Profiling
	p.Start()

	assert.Nil(t, p.server)
}

func TestProfiling_Start_InvalidPort(t *testing.T) {
	t.Setenv(PProfPortEnvVar, "not-a-number")

	var p Profiling
	p.Start()

	assert.Nil(t, p.server)
}

func TestProfiling_Start_Stop(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())

	t.Setenv(PProfPortEnvVar, strconv.Itoa(port))

	var p Profiling
	p.Start()
	require.NotNil(t, p.server)
	t.Cleanup(func() { p.Stop() })

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/debug/pprof/", port))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond)

	endpoints := []string{
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/threadcreate",
		"/debug/pprof/block",
		"/debug/pprof/mutex",
		"/debug/pprof/allocs",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, endpoint))
			require.NoError(t, err)
			defer resp.Body.Close()
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	p.Stop()
}

func TestProfiling_Start_PortAlreadyInUse(t *testing.T) {
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { lis.Close() })

	t.Setenv(PProfPortEnvVar, strconv.Itoa(port))

	var p Profiling
	p.Start()
	require.NotNil(t, p.server)

	// serve() logs an error when ListenAndServe fails on an occupied port.
	time.Sleep(50 * time.Millisecond)

	p.Stop()
}

func TestProfiling_Stop_ShutdownError(t *testing.T) {
	var p Profiling
	p.server = &http.Server{Addr: ":0"}
	assert.NotPanics(t, func() { p.Stop() })
}

func TestProfiling_Stop_NoServer(t *testing.T) {
	var p Profiling
	assert.NotPanics(t, func() { p.Stop() })
}
