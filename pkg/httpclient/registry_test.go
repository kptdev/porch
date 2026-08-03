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

package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nonHTTPTransport struct{}

func (nonHTTPTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrSkipAltProtocol
}

func TestSnapshotBaseTransport_FallbackWhenDefaultTransportIsNotHTTPTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })

	http.DefaultTransport = nonHTTPTransport{}

	transport := snapshotBaseTransport()
	require.NotNil(t, transport)
	assert.NotNil(t, transport.DialContext)
	assert.True(t, transport.ForceAttemptHTTP2)
	assert.Equal(t, 100, transport.MaxIdleConns)
}

func TestRegistryClientUsesHTTPTransport(t *testing.T) {
	client := &http.Client{Transport: RegistryTransport(nil)}
	require.NotNil(t, client)
	_, ok := client.Transport.(*http.Transport)
	assert.True(t, ok)
}

func TestRegistryClientAppliesTLSConfig(t *testing.T) {
	pool := x509.NewCertPool()
	tlsConfig := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}

	client := &http.Client{Transport: RegistryTransport(tlsConfig)}
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.EqualValues(t, tls.VersionTLS12, transport.TLSClientConfig.MinVersion)
	assert.Equal(t, pool, transport.TLSClientConfig.RootCAs)
}
