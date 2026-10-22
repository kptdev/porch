/*
Copyright 2026 The kpt Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

-- v1alpha2 PackageRevisionResources content store.
-- Coexists with legacy `resources` table (v1alpha1). No breaking changes.

CREATE TABLE IF NOT EXISTS package_revision_resources (
    k8s_name_space TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name       TEXT NOT NULL CHECK (k8s_name != ''),
    resource_key   TEXT NOT NULL CHECK (resource_key != ''),
    resource_value TEXT NOT NULL,
    PRIMARY KEY (k8s_name_space, k8s_name, resource_key)
);

-- Render-request coordination token per PR (not related to PackageRevision.revision field).
-- Used as render-request annotation token in v1alpha2 render handshake.
-- store_only_lifecycle marks an unpushed Draft or Proposed revision that exists only in
-- this store (push-drafts-to-git disabled). NULL means git owns it. It is a marker, not
-- a copy of the lifecycle (the CRD and git remain the record), and is cleared on publish.
CREATE TABLE IF NOT EXISTS prr_resource_versions (
    k8s_name_space TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name       TEXT NOT NULL CHECK (k8s_name != ''),
    resource_version TEXT NOT NULL,
    store_only_lifecycle TEXT CHECK (store_only_lifecycle IN ('Draft', 'Proposed')),
    PRIMARY KEY (k8s_name_space, k8s_name)
);

CREATE INDEX IF NOT EXISTS idx_prr_resources_lookup 
    ON package_revision_resources (k8s_name_space, k8s_name);

CREATE INDEX IF NOT EXISTS idx_prr_resource_versions_namespace
    ON prr_resource_versions (k8s_name_space);

-- Partial index: store-only revisions are few, so listing them stays cheap.
CREATE INDEX IF NOT EXISTS idx_prr_resource_versions_store_only
    ON prr_resource_versions (k8s_name_space)
    WHERE store_only_lifecycle IS NOT NULL;
