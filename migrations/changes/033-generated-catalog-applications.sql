ALTER TABLE catalog_applications DROP CONSTRAINT catalog_applications_origin_check;

ALTER TABLE catalog_applications
    ADD CONSTRAINT catalog_applications_origin_check
    CHECK (origin IN ('built-in', 'imported', 'generated'));

UPDATE catalog_applications
SET origin = 'generated', updated_at = now()
WHERE origin = 'imported'
  AND id LIKE 'dev.kubephos.mubench.%'
  AND descriptor #>> '{spec,materializer}' = 'io.kubephos.applications.mubench';
