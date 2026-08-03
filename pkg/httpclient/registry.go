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

// Package httpclient provides shared HTTP clients for outbound calls.
package httpclient

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// baseTransport is captured at init from http.DefaultTransport before any caller
// can replace it with a wrapper (e.g. for OpenTelemetry).
var baseTransport = snapshotBaseTransport()

func snapshotBaseTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func RegistryClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{Transport: RegistryTransport(tlsConfig)}
}

func RegistryTransport(tlsConfig *tls.Config) *http.Transport {
	t := baseTransport.Clone()
	if tlsConfig != nil {
		t.TLSClientConfig = tlsConfig.Clone()
	}
	return t
}

// OtelTransport returns an OpenTelemetry-instrumented transport for registry
// and other outbound HTTP calls. tlsConfig is applied to the underlying transport when non-nil.
func OtelTransport(tlsConfig *tls.Config) http.RoundTripper {
	return otelhttp.NewTransport(RegistryTransport(tlsConfig))
}
