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

package testutil

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"k8s.io/klog/v2"
)

// dbMavenMirrorEnv supplies a mirror for the embedded-postgres binary download,
// working around Cloudflare rate-limiting on repo1.maven.org.
const dbMavenMirrorEnv = "DB_MAVEN_MIRROR"

// embeddedPGUser/Password/Database are the fixed credentials for test instances.
const (
	embeddedPGUser     = "porch"
	embeddedPGPassword = "porch"
	embeddedPGDatabase = "porch"
)

// EmbeddedPostgres is a running embedded-postgres instance started for a test.
// Stop is registered with t.Cleanup, so callers only need the connection URL.
type EmbeddedPostgres struct {
	// URL is the connection string: postgresql://user:pass@localhost:port/db.
	URL string
	// Port is the dynamically-allocated port the instance listens on.
	Port int

	db *embeddedpostgres.EmbeddedPostgres
}

// freePort returns an OS-allocated free TCP port. Dynamic ports let the postgres test
// packages run in parallel without colliding on a hardcoded port.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// StartEmbeddedPostgres starts an isolated embedded-postgres instance and registers
// its shutdown with t.Cleanup. Per-test RuntimePath/DataPath plus a dynamic port let
// postgres test packages run in parallel: each Start() re-extracts into its own dir
// rather than clobbering the shared cache. CachePath stays default so the binary is
// downloaded/extracted once; DB_MAVEN_MIRROR overrides the download source.
func StartEmbeddedPostgres(t *testing.T) *EmbeddedPostgres {
	t.Helper()

	port := freePort(t)

	config := embeddedpostgres.DefaultConfig().
		Username(embeddedPGUser).
		Password(embeddedPGPassword).
		Database(embeddedPGDatabase).
		Port(uint32(port)).
		RuntimePath(t.TempDir()).
		DataPath(t.TempDir())

	if mavenMirror := os.Getenv(dbMavenMirrorEnv); mavenMirror != "" {
		config = config.BinaryRepositoryURL(mavenMirror)
	}

	db := embeddedpostgres.NewDatabase(config)
	if err := db.Start(); err != nil {
		t.Fatalf("could not start embedded postgres on port %d: %v", port, err)
	}

	t.Cleanup(func() {
		if err := db.Stop(); err != nil {
			klog.Errorf("stop of test database failed: %q", err)
		}
	})

	return &EmbeddedPostgres{
		URL:  fmt.Sprintf("postgresql://%s:%s@localhost:%d/%s", embeddedPGUser, embeddedPGPassword, port, embeddedPGDatabase),
		Port: port,
		db:   db,
	}
}

// ApplySchema reads a repo-root-relative SQL schema file and runs it via exec.
// The repo root comes from PORCHDIR, or by walking up to the go.mod directory.
func ApplySchema(t *testing.T, schemaFile string, exec func(sql string) error) {
	t.Helper()

	path := resolveRepoPath(t, schemaFile)
	sql, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read schema file %q: %v", path, err)
	}
	if err := exec(string(sql)); err != nil {
		t.Fatalf("failed to apply schema %q: %v", path, err)
	}
}

// resolveRepoPath resolves a repo-root-relative path to an absolute path.
func resolveRepoPath(t *testing.T, rel string) string {
	t.Helper()

	if filepath.IsAbs(rel) {
		return rel
	}

	if porchDir := os.Getenv("PORCHDIR"); porchDir != "" {
		return filepath.Join(porchDir, rel)
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.mod; fall back to the
			// relative path and let the caller surface a read error.
			return rel
		}
		dir = parent
	}
}
