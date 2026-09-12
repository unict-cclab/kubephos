ALTER TABLE experiments
    DROP CONSTRAINT experiments_configuration_id_fkey,
    ADD CONSTRAINT experiments_configuration_id_fkey
        FOREIGN KEY (configuration_id) REFERENCES experiment_configurations(id) ON DELETE SET NULL;

ALTER TABLE experiment_variants
    DROP CONSTRAINT experiment_variants_configuration_id_fkey,
    ADD CONSTRAINT experiment_variants_configuration_id_fkey
        FOREIGN KEY (configuration_id) REFERENCES experiment_configurations(id) ON DELETE SET NULL;
