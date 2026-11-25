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
-- Reverts the content deduplication of porch-db-1.6.0-1.6.4.sql: gives every file its full content back in resources.resource_value.
-- This grows the table back to its pre-deduplication size. Stop Porch while it runs, and run it with psql
-- WITHOUT -1/--single-transaction, because VACUUM FULL cannot run inside a transaction block.

ALTER TABLE resources ADD COLUMN IF NOT EXISTS resource_value TEXT;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'resources' AND column_name = 'content_hash'
                 AND table_schema = current_schema()) THEN
        UPDATE resources r SET resource_value = b.value
            FROM resource_blobs b
            WHERE r.content_hash = b.content_hash AND r.resource_value IS NULL;

        ALTER TABLE resources ALTER COLUMN resource_value SET NOT NULL;
        DROP INDEX IF EXISTS idx_resources_content_hash;
        ALTER TABLE resources DROP COLUMN content_hash;
    END IF;
END
$$;

-- resource_blobs is shared content storage. Keep it if any other table still has a content_hash column
-- pointing into it; dropping it would delete that table's content.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE column_name = 'content_hash' AND table_name <> 'resource_blobs'
                     AND table_schema = current_schema()) THEN
        DROP TABLE IF EXISTS resource_blobs;
    END IF;
END
$$;

VACUUM FULL resources;

DROP INDEX IF EXISTS idx_pr_k8s_name_workspace;
DROP INDEX IF EXISTS idx_pr_meta_labels_gin;
DROP INDEX IF EXISTS idx_pr_spec_pkgmeta_labels_gin;
DROP INDEX IF EXISTS idx_packages_k8s_name_pkg;

ALTER TABLE package_revisions
    DROP COLUMN IF EXISTS last_pushed_db_updated;
