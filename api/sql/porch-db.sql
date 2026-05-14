/*
Copyright 2024-2026 The kpt Authors

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
CREATE TABLE IF NOT EXISTS repositories (
    k8s_name_space  TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name        TEXT NOT NULL CHECK (k8s_name != ''),
    directory       TEXT NOT NULL,
    default_ws_name TEXT NOT NULL,
    meta            TEXT NOT NULL,
    spec            TEXT NOT NULL,
    updated         TIMESTAMP,
    updatedby       TEXT,
    deployment      BOOLEAN,
    PRIMARY KEY (k8s_name_space, k8s_name)
);

CREATE OR REPLACE FUNCTION check_immutable_repositories_columns() RETURNS trigger
    LANGUAGE plpgsql AS
$BODY$
BEGIN
    IF NEW.directory != OLD.directory OR NEW.default_ws_name != OLD.default_ws_name THEN
        RAISE EXCEPTION 'create or update not allowed on immutable columns "directory" and "default_ws_name"';
    END IF;
    RETURN NEW;
END;
$BODY$;

CREATE OR REPLACE TRIGGER immutable_repositories_columns
   BEFORE UPDATE ON repositories FOR EACH ROW
   EXECUTE PROCEDURE check_immutable_repositories_columns();

CREATE TABLE IF NOT EXISTS packages (
    k8s_name_space TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name       TEXT NOT NULL CHECK (k8s_name != ''),
    repo_k8s_name  TEXT NOT NULL,
    package_path   TEXT NOT NULL,
    meta           TEXT NOT NULL,
    spec           TEXT NOT NULL,
    updated        TIMESTAMP NOT NULL,
    updatedby      TEXT NOT NULL,
    PRIMARY KEY (k8s_name_space, k8s_name),
    CONSTRAINT fk_repository
        FOREIGN KEY (k8s_name_space, repo_k8s_name)
        REFERENCES repositories (k8s_name_space, k8s_name)
        ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION check_immutable_packages_columns() RETURNS trigger
    LANGUAGE plpgsql AS
$BODY$
BEGIN
    IF NEW.repo_k8s_name != OLD.repo_k8s_name OR NEW.package_path != OLD.package_path THEN
        RAISE EXCEPTION 'create or create or update not allowed on immutable columns "repo_k8s_name" and "package_path"';
    END IF;
    RETURN NEW;
END;
$BODY$;

CREATE OR REPLACE TRIGGER immutable_packages_columns
   BEFORE UPDATE ON packages FOR EACH ROW
   EXECUTE PROCEDURE check_immutable_packages_columns();

CREATE TABLE IF NOT EXISTS package_revisions (
    k8s_name_space   TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name         TEXT NOT NULL CHECK (k8s_name != ''),
    package_k8s_name TEXT NOT NULL,
    revision         INTEGER NOT NULL,
    meta             TEXT NOT NULL,
    spec             TEXT NOT NULL,
    updated          TIMESTAMP NOT NULL,
    updatedby        TEXT NOT NULL,
    lifecycle        TEXT CHECK (lifecycle IN ('Draft', 'Proposed', 'Published', 'DeletionProposed')) NOT NULL,
    ext_pr_id        TEXT NOT NULL,
    latest           BOOLEAN NOT NULL DEFAULT FALSE,
    tasks            TEXT NOT NULL,
    kptfile_status   TEXT NOT NULL DEFAULT '{}',
    resources_size   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (k8s_name_space, k8s_name),
    CONSTRAINT fk_package
        FOREIGN KEY (k8s_name_space, package_k8s_name)
        REFERENCES packages (k8s_name_space, k8s_name)
        ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION check_package_revisions_columns() RETURNS trigger
    LANGUAGE plpgsql AS
$BODY$
DECLARE
    lifecycle_draft             CONSTANT VARCHAR(5)  := 'Draft';
    lifecycle_proposed          CONSTANT VARCHAR(8)  := 'Proposed';
    lifecycle_published         CONSTANT VARCHAR(9)  := 'Published';
    lifecycle_deletion_proposed CONSTANT VARCHAR(16) := 'DeletionProposed';

    count_revisions INTEGER;
    latest_revision INTEGER;
BEGIN
    IF NEW.package_k8s_name != OLD.package_k8s_name THEN
        RAISE EXCEPTION 'create or update not allowed on immutable column "package_k8s_name"';
    END IF;

    IF NEW.revision < -1 OR OLD.revision < -1 THEN
        RAISE EXCEPTION 'create or update not allowed on column "revision", revisions of less than -1 are not allowed';
    END IF;

    -- Package revisions in external repositories with uncontrolled revisions must always be 'Published' and cannot have lifecycle changes
    IF NEW.revision = -1 OR OLD.revision = -1 THEN
        IF NOT (NEW.lifecycle = lifecycle_published OR NEW.lifecycle = lifecycle_deletion_proposed) OR NOT (OLD.lifecycle = lifecycle_published OR OLD.lifecycle = lifecycle_deletion_proposed) THEN
            RAISE EXCEPTION 'create or update not allowed on column "lifecycle", lifecycle of % illegal, package revision has revision -1', NEW.lifecycle;
        ELSE
            return NEW;
        END IF;
    END IF;

    -- Package revisions with revision 0 are draft package revisions
    IF NEW.revision = 0 THEN
        IF OLD.revision != 0 THEN
            RAISE EXCEPTION 'create or update not allowed on column "revision", update of revision from % to zero is illegal', OLD.revision;
        END IF;

        IF NOT (NEW.lifecycle = lifecycle_draft OR NEW.lifecycle = lifecycle_proposed) OR NOT (OLD.lifecycle = lifecycle_draft OR OLD.lifecycle = lifecycle_proposed) THEN
            RAISE EXCEPTION 'create or update not allowed on column "revision", revision value of 0 is only allowed on when lifecycle is Draft or Proposed';
        END IF;

        return NEW;
    END IF;

    IF NEW.lifecycle = lifecycle_draft THEN
        RAISE EXCEPTION 'create or update not allowed on column "revision", revision of % on drafts is illegal', NEW.revision;
    END IF;

    IF NEW.revision = OLD.revision THEN
        IF NEW.lifecycle = OLD.lifecycle THEN
            return NEW;
        END IF;

        IF NEW.lifecycle = lifecycle_published OR NEW.lifecycle = lifecycle_deletion_proposed THEN
            return NEW;
        END IF;

        RAISE EXCEPTION 'create or update not allowed on column "lifecycle", change from % to % is illegal', OLD.lifecycle, NEW.lifecycle;
    END IF;

    IF OLD.revision != 0 THEN
        RAISE EXCEPTION 'create or update not allowed on column "revision", update of revision from % to % is illegal', OLD.revision, NEW.revision;
    END IF;

    IF OLD.lifecycle != lifecycle_proposed THEN
        RAISE EXCEPTION 'create or update not allowed on column "revision", lifecycle % is not Proposed', OLD.lifecycle;
    END IF;

    count_revisions := (SELECT COUNT(revision) FROM package_revisions WHERE k8s_name_space = NEW.k8s_name_space AND package_k8s_name = NEW.package_k8s_name AND revision = NEW.revision);
    IF count_revisions > 0 THEN
       RAISE EXCEPTION 'create or update not allowed on column "revision", revision % already exists', NEW.revision;
    END IF;

    latest_revision := (SELECT MAX(revision) FROM package_revisions WHERE k8s_name_space = NEW.k8s_name_space AND package_k8s_name = NEW.package_k8s_name AND revision > 0);

    IF NEW.lifecycle = lifecycle_published AND NEW.revision > 0 AND (latest_revision IS NULL OR NEW.revision >= latest_revision) THEN
        UPDATE package_revisions SET latest = FALSE WHERE k8s_name_space = NEW.k8s_name_space AND package_k8s_name = NEW.package_k8s_name AND latest;
        NEW.latest = TRUE;
    END IF;

    RETURN NEW;
END;
$BODY$;

CREATE OR REPLACE TRIGGER package_revisions_columns
   BEFORE INSERT OR UPDATE ON package_revisions FOR EACH ROW
   EXECUTE PROCEDURE check_package_revisions_columns();

CREATE OR REPLACE FUNCTION check_package_revisions_delete() RETURNS trigger
    LANGUAGE plpgsql AS
$BODY$
DECLARE
    lifecycle_published         CONSTANT VARCHAR(9)  := 'Published';
    lifecycle_deletion_proposed CONSTANT VARCHAR(16) := 'DeletionProposed';

    latest_revision INTEGER;
BEGIN
    latest_revision := (SELECT MAX(revision) FROM package_revisions WHERE k8s_name_space = OLD.k8s_name_space AND package_k8s_name = OLD.package_k8s_name AND revision > 0);
    IF latest_revision IS NOT NULL THEN
        UPDATE package_revisions SET latest = TRUE WHERE k8s_name_space = OLD.k8s_name_space AND package_k8s_name = OLD.package_k8s_name AND revision = latest_revision AND (lifecycle = lifecycle_published OR lifecycle = lifecycle_deletion_proposed);
    END IF;
    RETURN OLD;
END;
$BODY$;

CREATE OR REPLACE TRIGGER package_revisions_delete
   AFTER DELETE ON package_revisions FOR EACH ROW
   EXECUTE PROCEDURE check_package_revisions_delete();

CREATE TABLE IF NOT EXISTS resources (
    k8s_name_space TEXT NOT NULL CHECK (k8s_name_space != ''),
    k8s_name       TEXT NOT NULL CHECK (k8s_name != ''),
    revision       INTEGER NOT NULL,
    resource_key   TEXT NOT NULL CHECK (resource_key != ''),
    resource_value TEXT NOT NULL,
    PRIMARY KEY (k8s_name_space, k8s_name, resource_key),
    CONSTRAINT fk_package_rev
        FOREIGN KEY (k8s_name_space, k8s_name)
        REFERENCES package_revisions (k8s_name_space, k8s_name)
        ON DELETE CASCADE
);
