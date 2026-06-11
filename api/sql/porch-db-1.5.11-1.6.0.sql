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

-- Add the upstream_ref_name column to store the upstream package revision name
-- extracted from tasks. This replaces the regex-based search on the tasks column
-- with an indexed lookup for findUpstreamRefsFromDB.
--
-- Existing rows default to ''. The Porch server automatically backfills
-- this column on startup by parsing the tasks JSON and extracting the
-- upstream reference name from clone/upgrade tasks.
-- No manual resync is required.
ALTER TABLE package_revisions
    ADD COLUMN IF NOT EXISTS upstream_ref_name TEXT NOT NULL DEFAULT '';

-- Create a partial B-tree index for fast upstream reference lookups.
-- Only indexes rows that have a non-empty upstream_ref_name and are not
-- auto-managed main branch packages (revision != -1).
CREATE INDEX IF NOT EXISTS idx_package_revisions_upstream_ref
    ON package_revisions (k8s_name_space, upstream_ref_name)
    WHERE upstream_ref_name != '' AND revision != -1;

-- Hot path: "Get the latest revision of a package" -- issued by every rpkg_approve.
CREATE INDEX IF NOT EXISTS idx_package_revisions_pkg_revdesc
    ON package_revisions (k8s_name_space, package_k8s_name, revision DESC);

-- Hot path: "List PRs in lifecycle X" -- background reconcilers, dashboards,
-- and DeletionProposed sweeps.
CREATE INDEX IF NOT EXISTS idx_package_revisions_lifecycle
    ON package_revisions (lifecycle);

-- Hot path: "Find the latest=true revision of a package" -- partial index
-- saves ~50% storage vs a full index since most revisions have latest=false.
CREATE INDEX IF NOT EXISTS idx_package_revisions_latest_partial
    ON package_revisions (k8s_name_space, package_k8s_name)
    WHERE latest = true;

-- Hot path: "List all packages in a repository" -- per-repo reconciliation,
-- runs every sync.schedule cycle.
CREATE INDEX IF NOT EXISTS idx_packages_repo
    ON packages (k8s_name_space, repo_k8s_name);
