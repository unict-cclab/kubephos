ALTER TABLE strategy_catalog DROP CONSTRAINT strategy_catalog_kind_check;
ALTER TABLE strategy_catalog ADD CONSTRAINT strategy_catalog_kind_check CHECK (kind IN ('scheduler', 'descheduler', 'autoscaler', 'orchestrator'));
