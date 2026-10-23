-- Preserve saved definitions and immutable run snapshots for historical results.
-- Removed controllers must never silently become a default scheduling strategy.
WITH unavailable AS (
    SELECT id FROM experiment_configurations
    WHERE EXISTS (
        SELECT 1 FROM jsonb_array_elements(COALESCE(definition->'components', '[]'::jsonb)) component
        WHERE component->>'capability' LIKE 'orchestrator.%'
    )
), affected AS (
    SELECT id FROM unavailable
    UNION
    SELECT suite.id FROM experiment_configurations suite
    WHERE EXISTS (
        SELECT 1 FROM jsonb_array_elements(COALESCE(suite.definition->'variants', '[]'::jsonb)) member
        JOIN unavailable ON unavailable.id = member->>'configurationId'
    )
)
UPDATE experiment_configurations
SET validation = jsonb_build_object(
    'valid', false,
    'checkedAt', now(),
    'issues', jsonb_build_array(jsonb_build_object(
        'level', 'error', 'path', 'components',
        'message', 'A configured component is no longer available. Remove or replace it before running.'
    ))
)
WHERE id IN (SELECT id FROM affected);

DELETE FROM strategy_catalog WHERE kind = 'orchestrator';
ALTER TABLE strategy_catalog DROP CONSTRAINT strategy_catalog_kind_check;
ALTER TABLE strategy_catalog ADD CONSTRAINT strategy_catalog_kind_check
    CHECK (kind IN ('scheduler', 'descheduler', 'autoscaler'));
