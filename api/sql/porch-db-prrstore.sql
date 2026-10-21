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
CREATE TABLE IF NOT EXISTS prr_resource_versions (
    k8s_name_space TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name       TEXT NOT NULL CHECK (k8s_name != ''),
    resource_version TEXT NOT NULL,
    PRIMARY KEY (k8s_name_space, k8s_name)
);

CREATE INDEX IF NOT EXISTS idx_prr_resources_lookup 
    ON package_revision_resources (k8s_name_space, k8s_name);

CREATE INDEX IF NOT EXISTS idx_prr_resource_versions_namespace
    ON prr_resource_versions (k8s_name_space);
