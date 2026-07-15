// Copyright 2025 The Nephio Authors
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
	"context"
	"fmt"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"time"

	"k8s.io/klog/v2"
)

const PProfPortEnvVar = "PORCH_PPROF_PORT"

type Profiling struct {
	port   int
	server *http.Server
}

func (p *Profiling) Start() {
	if envport := os.Getenv(PProfPortEnvVar); envport != "" {
		parsed, err := strconv.Atoi(envport)
		if err != nil {
			klog.Warningf("Failed to parse %s environment variable (%s) as int: %v", PProfPortEnvVar, envport, err)
			return
		}
		p.port = parsed
	} else {
		return
	}

	// Enable profiling for mutex and block profiles
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	mux := http.NewServeMux()

	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	mux.Handle("/debug/pprof/heap", httppprof.Handler("heap"))
	mux.Handle("/debug/pprof/goroutine", httppprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/threadcreate", httppprof.Handler("threadcreate"))
	mux.Handle("/debug/pprof/block", httppprof.Handler("block"))
	mux.Handle("/debug/pprof/mutex", httppprof.Handler("mutex"))
	mux.Handle("/debug/pprof/allocs", httppprof.Handler("allocs"))
	p.server = &http.Server{
		Addr:              fmt.Sprintf(":%d", p.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go p.serve()
}

func (p *Profiling) Stop() {
	if p.server != nil {
		klog.Infof("Shutting down profiling server")
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(3*time.Second))
		defer cancel()
		err := p.server.Shutdown(ctx)
		if err != nil {
			klog.Errorf("Error shutting down profiling server: %v", err)
		}
	}
}

func (p *Profiling) serve() {
	klog.Infof("Starting profiling server on port :%d", p.port)
	if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		klog.Errorf("Error starting metrics server: %v", err)
	}
	klog.Info("Profiling server stopped")
}
