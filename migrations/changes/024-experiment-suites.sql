ALTER TABLE experiments
    ADD COLUMN kind text NOT NULL DEFAULT 'comparison',
    ADD CONSTRAINT experiments_kind_check CHECK (kind IN ('comparison', 'instance', 'suite'));

ALTER TABLE experiment_variants
    ADD COLUMN configuration_id text REFERENCES experiment_configurations(id) ON DELETE RESTRICT;

CREATE INDEX experiment_variants_configuration_idx ON experiment_variants(configuration_id) WHERE configuration_id IS NOT NULL;
