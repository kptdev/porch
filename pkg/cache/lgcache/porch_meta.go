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

package lgcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/kptdev/porch/api/porch/key"
)

type porchMeta struct {
	LastSyncedRemoteVersion string   `json:"lastSyncedRemoteVersion,omitempty"`
	PushedKeys              []string `json:"pushedKeys,omitempty"`
}

func (r *lgRepository) metaPath() string {
	return filepath.Join(r.pvcRoot, "meta", r.repoKey.Namespace, r.repoKey.Name+".json")
}

func (r *lgRepository) loadMeta() porchMeta {
	b, err := os.ReadFile(r.metaPath())
	if err != nil {
		return porchMeta{}
	}
	var m porchMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return porchMeta{}
	}
	return m
}

func (r *lgRepository) saveMeta(m porchMeta) error {
	path := r.metaPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (r *lgRepository) wasPushed(k key.PackageRevisionKey) bool {
	name := k.K8SName()
	return slices.Contains(r.loadMeta().PushedKeys, name)
}

func (r *lgRepository) markPushed(k key.PackageRevisionKey) {
	m := r.loadMeta()
	name := k.K8SName()
	if slices.Contains(m.PushedKeys, name) {
		return
	}
	m.PushedKeys = append(m.PushedKeys, name)
	_ = r.saveMeta(m)
}

func (r *lgRepository) unmarkPushed(k key.PackageRevisionKey) {
	m := r.loadMeta()
	name := k.K8SName()
	out := m.PushedKeys[:0]
	for _, pushed := range m.PushedKeys {
		if pushed != name {
			out = append(out, pushed)
		}
	}
	m.PushedKeys = out
	_ = r.saveMeta(m)
}
