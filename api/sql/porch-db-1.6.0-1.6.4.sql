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

-- Deduplicate package revision file contents.
--
-- Before: resources(k8s_name_space, k8s_name, revision, resource_key, resource_value) held every file of
-- every package revision in full, so identical files in many revisions or clones were stored many times.
-- After:  resources holds a SHA-256 content_hash per file, and resource_blobs holds each distinct content once.
--
-- The hash is sha256(convert_to(resource_value, 'UTF8')), which matches sha256.Sum256([]byte(value)) in Go.
-- Requires PostgreSQL 11 or later (built-in sha256()) and a UTF8 database.
--
-- This drops resources.resource_value, so Porch (server and controllers) must be stopped while it runs and
-- started again on the new version afterwards. Run it with psql WITHOUT -1/--single-transaction, because
-- VACUUM FULL cannot run inside a transaction block.

CREATE TABLE IF NOT EXISTS resource_blobs (
    content_hash BYTEA     PRIMARY KEY CHECK (octet_length(content_hash) = 32),
    value        TEXT      NOT NULL,
    created      TIMESTAMP NOT NULL DEFAULT now()
);

ALTER TABLE resources ADD COLUMN IF NOT EXISTS content_hash BYTEA;

-- Only runs if the old column still exists, so the script can be re-run after a partial failure.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'resources' AND column_name = 'resource_value'
                 AND table_schema = current_schema()) THEN
        INSERT INTO resource_blobs (content_hash, value)
            SELECT DISTINCT ON (h) h, v
            FROM (SELECT sha256(convert_to(resource_value, 'UTF8')) AS h, resource_value AS v
                  FROM resources) s
            ON CONFLICT (content_hash) DO NOTHING;

        UPDATE resources SET content_hash = sha256(convert_to(resource_value, 'UTF8'))
            WHERE content_hash IS NULL;

        ALTER TABLE resources ALTER COLUMN content_hash SET NOT NULL;
        ALTER TABLE resources DROP COLUMN resource_value;
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_resources_content_hash ON resources (content_hash);

-- Dropping a column does not give the disk space back; rewrite the table so the old values are released.
VACUUM FULL resources;
ANALYZE resources;
ANALYZE resource_blobs;
