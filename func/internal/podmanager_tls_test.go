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

package internal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTlsCACertPath(t *testing.T) {
	tests := map[string]struct {
		files    map[string][]byte
		wantFile string
	}{
		"prefers ca.crt over ca.pem": {
			files: map[string][]byte{
				"ca.crt": []byte("crt"),
				"ca.pem": []byte("pem"),
			},
			wantFile: "ca.crt",
		},
		"falls back to ca.pem": {
			files: map[string][]byte{
				"ca.pem": []byte("pem"),
			},
			wantFile: "ca.pem",
		},
		"falls back to cacert.pem": {
			files: map[string][]byte{
				"cacert.pem": []byte("cacert"),
			},
			wantFile: "cacert.pem",
		},
		"falls back to ca-bundle.crt": {
			files: map[string][]byte{
				"ca-bundle.crt": []byte("bundle"),
			},
			wantFile: "ca-bundle.crt",
		},
		"falls back to root.crt": {
			files: map[string][]byte{
				"root.crt": []byte("root"),
			},
			wantFile: "root.crt",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, content := range tc.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), content, 0o600))
			}

			path, err := tlsCACertPath(dir)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(dir, tc.wantFile), path)
		})
	}
}

func TestTlsCACertPathMissingMount(t *testing.T) {
	_, err := tlsCACertPath(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "tls secret folder")
}

func TestTlsCACertPathNoCandidates(t *testing.T) {
	_, err := tlsCACertPath(t.TempDir())
	require.Error(t, err)
	assert.ErrorContains(t, err, "no CA certificate found")
	assert.ErrorContains(t, err, "ca.crt")
}

func TestLoadTLSConfig(t *testing.T) {
	tests := map[string]struct {
		writeCert func(t *testing.T) string
		wantErr   string
	}{
		"valid PEM certificate": {
			writeCert: func(t *testing.T) string {
				t.Helper()
				path := filepath.Join(t.TempDir(), "ca.pem")
				require.NoError(t, os.WriteFile(path, generateSelfSignedCertPEM(t), 0o600))
				return path
			},
		},
		"invalid PEM data": {
			writeCert: func(t *testing.T) string {
				t.Helper()
				path := filepath.Join(t.TempDir(), "ca.pem")
				require.NoError(t, os.WriteFile(path, []byte("not a valid PEM certificate"), 0o600))
				return path
			},
			wantErr: "failed to append certificates",
		},
		"missing file": {
			writeCert: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "missing.pem")
			},
			wantErr: "no such file",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			caCertPath := tc.writeCert(t)

			tlsConfig, err := loadTLSConfig(caCertPath)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, tlsConfig)
				assert.NotNil(t, tlsConfig.RootCAs)
				assert.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
			}
		})
	}
}

func TestMakeTlsTransport(t *testing.T) {
	tests := map[string]struct {
		setup       func(t *testing.T) string
		errContains string
	}{
		"returns transport when ca.crt is valid": {
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "ca.crt"), generateSelfSignedCertPEM(t), 0o600))
				return dir
			},
		},
		"returns error when secret path is missing": {
			setup: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "missing")
			},
			errContains: "tls secret folder",
		},
		"returns error when no CA certificate found": {
			setup: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			errContains: "no CA certificate found",
		},
		"returns error when CA PEM is invalid": {
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("not a cert"), 0o600))
				return dir
			},
			errContains: "failed to append certificates",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			transport, err := makeTlsTransport(tc.setup(t))
			if tc.errContains != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.errContains)
				assert.Nil(t, transport)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, transport)
			}
		})
	}
}

func TestOtelTransport(t *testing.T) {
	tests := map[string]struct {
		tlsConfig *tls.Config
	}{
		"nil tls config wraps default transport": {
			tlsConfig: nil,
		},
		"applies provided tls config": {
			tlsConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    x509.NewCertPool(),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			transport := otelTransport(tc.tlsConfig)
			assert.NotNil(t, transport)
		})
	}
}

func generateSelfSignedCertPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageCertSign,
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}
