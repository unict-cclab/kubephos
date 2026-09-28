ALTER TABLE strategy_catalog
    ALTER COLUMN harbor_resource_id DROP NOT NULL,
    ALTER COLUMN operation_id DROP NOT NULL;
