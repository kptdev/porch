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

ALTER TABLE package_revisions
    ADD COLUMN IF NOT EXISTS last_pushed_db_updated TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_packages_k8s_name_pkg
    ON packages (k8s_name_space, split_part(k8s_name, '.', -1));
CREATE INDEX IF NOT EXISTS idx_pr_spec_pkgmeta_labels_gin
    ON package_revisions USING gin ((spec::jsonb->'packageMetadata'->'labels'));
CREATE INDEX IF NOT EXISTS idx_pr_meta_labels_gin
    ON package_revisions USING gin ((meta::jsonb->'labels'));
CREATE INDEX IF NOT EXISTS idx_pr_k8s_name_workspace
    ON package_revisions (k8s_name_space, substring(k8s_name from length(package_k8s_name)+2));
