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

DROP INDEX IF EXISTS idx_pr_k8s_name_workspace;
DROP INDEX IF EXISTS idx_pr_meta_labels_gin;
DROP INDEX IF EXISTS idx_pr_spec_pkgmeta_labels_gin;
DROP INDEX IF EXISTS idx_packages_k8s_name_pkg;

ALTER TABLE package_revisions
    DROP COLUMN IF EXISTS last_pushed_db_updated;
