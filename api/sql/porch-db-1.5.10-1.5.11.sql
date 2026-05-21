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

-- Add the resources_size column - stores the total size of a package revision's
-- resource files in bytes so it can be found without having to retrieve the full
-- resources.
ALTER TABLE package_revisions
    ADD COLUMN IF NOT EXISTS resources_size BIGINT NOT NULL DEFAULT 0;

-- In the event of an upgrade with repositories already synced, Porch's sync
-- routines (manual or background) will not detect the need to backfill
-- resource sizes for existing package revisions.
-- Go through them once, calculating their resource sizes, and backfill the
-- resources_size column.
UPDATE package_revisions pr
SET resources_size = r.total_size
FROM (
    SELECT k8s_name_space, k8s_name, revision, SUM(OCTET_LENGTH(resource_value)) AS total_size
    FROM resources
    GROUP BY k8s_name_space, k8s_name, revision
) r
WHERE pr.k8s_name_space = r.k8s_name_space
  AND pr.k8s_name = r.k8s_name
  AND pr.revision = r.revision;