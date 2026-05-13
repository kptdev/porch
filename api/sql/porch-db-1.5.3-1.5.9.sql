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

-- Add the kptfile_status column to cache Kptfile-derived status fields
-- (conditions, upstreamLock) needed for serving PackageRevision API queries
-- without re-parsing the Kptfile YAML on every read.
--
-- Existing rows default to '{}'. The Porch server automatically backfills
-- this column on startup by reading each Kptfile resource from the resources
-- table, parsing it, and storing the extracted status. It also backfills
-- the spec column with readinessGates and packageMetadata from the Kptfile.
-- No manual resync is required.

ALTER TABLE package_revisions
    ADD COLUMN IF NOT EXISTS kptfile_status TEXT NOT NULL DEFAULT '{}';

-- Fix timezone handling for timestamp columns to ensure consistent UTC storage
-- This is critical when porch-server runs in non-UTC timezones (e.g., IST)
-- Convert existing timestamp columns to store UTC timezone explicitly

ALTER TABLE repositories
    ALTER COLUMN updated TYPE TIMESTAMPTZ USING updated AT TIME ZONE 'UTC';

ALTER TABLE packages
    ALTER COLUMN updated TYPE TIMESTAMPTZ USING updated AT TIME ZONE 'UTC';

ALTER TABLE package_revisions
    ALTER COLUMN updated TYPE TIMESTAMPTZ USING updated AT TIME ZONE 'UTC';
