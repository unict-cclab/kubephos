-- Keep the most recently updated entry for each application ID.
-- Experiment snapshots and their digests remain intact for historical results.
DELETE FROM catalog_applications old
USING catalog_applications newer
WHERE old.id = newer.id AND
 (old.enabled, old.updated_at, old.created_at, old.version) <
 (newer.enabled, newer.updated_at, newer.created_at, newer.version);

UPDATE experiment_configurations
SET application_ref = split_part(application_ref, '@', 1)
WHERE application_ref LIKE 'app:%@%';

DROP TABLE catalog_application_previews;
ALTER TABLE catalog_applications DROP CONSTRAINT catalog_applications_pkey;
DROP INDEX catalog_applications_name_idx;
ALTER TABLE catalog_applications DROP COLUMN version;
UPDATE catalog_applications SET descriptor = descriptor #- '{metadata,version}';
ALTER TABLE catalog_applications ADD PRIMARY KEY (id);
CREATE INDEX catalog_applications_name_idx ON catalog_applications(name);
CREATE TABLE catalog_application_previews (
 application_id text PRIMARY KEY REFERENCES catalog_applications(id) ON DELETE CASCADE,
 application_digest text NOT NULL,
 content jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
