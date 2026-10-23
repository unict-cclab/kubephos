ALTER TABLE experiments ADD COLUMN configuration_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb;

-- Historical suites can outlive both their source configurations and cluster.
-- Preserve them as editable recipes; single configurations still require a cluster.
ALTER TABLE experiment_configurations ALTER COLUMN cluster_resource_id DROP NOT NULL;
ALTER TABLE experiment_configurations ADD CONSTRAINT configuration_single_cluster_check
 CHECK (cluster_resource_id IS NOT NULL OR COALESCE(definition->>'kind', 'single') = 'suite');

WITH legacy_suites AS (
 SELECT e.*, row_number() OVER (PARTITION BY owner_user_id, lower(name) ORDER BY created_at, id) AS name_rank
 FROM experiments e WHERE e.kind = 'suite' AND e.configuration_id IS NULL
)
INSERT INTO experiment_configurations (id, workspace_id, cluster_resource_id, owner_user_id, name, description, application_ref, application_digest, definition, validation, created_at, updated_at)
SELECT 'expcfg_suite_' || md5(e.id), e.workspace_id, anchor.cluster_id, e.owner_user_id,
 CASE WHEN e.name_rank = 1 AND NOT EXISTS (SELECT 1 FROM experiment_configurations existing WHERE existing.owner_user_id = e.owner_user_id AND lower(existing.name) = lower(e.name)) THEN e.name ELSE left(e.name, 78) || ' · ' || md5(e.id) END, e.description, COALESCE(anchor.application_ref, ''), COALESCE(anchor.application_digest, ''),
 jsonb_build_object('kind', 'suite', 'runs', COALESCE(repetitions.runs, 1), 'variants', members.variants),
 jsonb_build_object('valid', members.valid AND anchor.cluster_id IS NOT NULL, 'issues', CASE WHEN members.valid AND anchor.cluster_id IS NOT NULL THEN '[]'::jsonb ELSE '[{"level":"error","path":"definition.variants","message":"A historical source configuration is unavailable. Select at least two existing single configurations before running this suite."}]'::jsonb END),
 e.created_at, e.updated_at
FROM legacy_suites e
CROSS JOIN LATERAL (
 SELECT COALESCE(jsonb_agg(jsonb_build_object('configurationId', COALESCE(v.configuration_id, ''), 'alias', COALESCE(NULLIF(v.display_alias, ''), v.name), 'color', COALESCE(v.plot_color, (ARRAY['#1f77b4','#d55e00','#009e73','#cc79a7','#e69f00','#56b4e9','#000000','#7f7f7f'])[((v.position - 1) % 8) + 1])) ORDER BY v.position), '[]'::jsonb) AS variants,
 count(*) BETWEEN 2 AND 8 AND bool_and(c.id IS NOT NULL) AS valid
 FROM experiment_variants v LEFT JOIN experiment_configurations c ON c.id = v.configuration_id
 WHERE v.experiment_id = e.id
) members
LEFT JOIN LATERAL (
 SELECT COALESCE(c.cluster_resource_id, r.cluster_resource_id) AS cluster_id, c.application_ref, c.application_digest
 FROM experiment_variants v
 LEFT JOIN experiment_configurations c ON c.id = v.configuration_id
 LEFT JOIN experiment_trials t ON t.variant_id = v.id
 LEFT JOIN pipeline_runs r ON r.id = t.pipeline_run_id
 WHERE v.experiment_id = e.id ORDER BY v.position, t.position LIMIT 1
) anchor ON true
LEFT JOIN LATERAL (
 SELECT max(counts.runs)::int AS runs FROM (
 SELECT count(*) AS runs FROM experiment_variants v JOIN experiment_trials t ON t.variant_id = v.id
 WHERE v.experiment_id = e.id GROUP BY v.id
 ) counts
) repetitions ON true
WHERE e.kind = 'suite' AND e.configuration_id IS NULL;

UPDATE experiments e SET configuration_id = c.id
FROM experiment_configurations c
WHERE e.kind = 'suite' AND e.configuration_id IS NULL AND c.id = 'expcfg_suite_' || md5(e.id);

-- Backfill only information that was actually recorded for historical runs.
-- The variant definitions are the original execution snapshots, not today's configs.
UPDATE experiments e SET configuration_snapshot = jsonb_build_object(
 'kind', e.kind, 'name', e.name, 'description', e.description, 'scheduledFor', e.scheduled_for,
 'configurationId', e.configuration_id,
 'variants', COALESCE((SELECT jsonb_agg(jsonb_build_object('configurationId', v.configuration_id, 'name', v.name, 'alias', v.display_alias, 'color', v.plot_color, 'definition', v.configuration, 'runs', (SELECT count(*) FROM experiment_trials t WHERE t.variant_id = v.id)) ORDER BY v.position) FROM experiment_variants v WHERE v.experiment_id = e.id), '[]'::jsonb)
);

CREATE TABLE configuration_suite_members (
 suite_id text NOT NULL REFERENCES experiment_configurations(id) ON DELETE CASCADE,
 configuration_id text NOT NULL REFERENCES experiment_configurations(id) ON DELETE RESTRICT,
 PRIMARY KEY (suite_id, configuration_id), CHECK (suite_id <> configuration_id)
);
CREATE INDEX configuration_suite_members_source_idx ON configuration_suite_members(configuration_id);
INSERT INTO configuration_suite_members (suite_id, configuration_id)
SELECT DISTINCT suite.id, member.id
FROM experiment_configurations suite
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(suite.definition->'variants', '[]'::jsonb)) entry
JOIN experiment_configurations member ON member.id = entry->>'configurationId'
WHERE suite.definition->>'kind' = 'suite' AND COALESCE(member.definition->>'kind', 'single') = 'single';

CREATE FUNCTION sync_configuration_suite_members() RETURNS trigger AS $$
DECLARE member_id text; member_kind text;
BEGIN
 IF TG_OP = 'UPDATE' AND OLD.definition = NEW.definition THEN RETURN NEW; END IF;
 IF NEW.definition->>'kind' = 'suite' THEN
  IF EXISTS (SELECT 1 FROM configuration_suite_members WHERE configuration_id = NEW.id) THEN
   RAISE EXCEPTION 'a suite member must remain a single configuration' USING ERRCODE = '23514';
  END IF;
 END IF;
 DELETE FROM configuration_suite_members WHERE suite_id = NEW.id;
 IF NEW.definition->>'kind' = 'suite' THEN
  FOR member_id IN SELECT value->>'configurationId' FROM jsonb_array_elements(NEW.definition->'variants') LOOP
   SELECT COALESCE(definition->>'kind', 'single') INTO member_kind FROM experiment_configurations WHERE id = member_id FOR SHARE;
   IF member_kind = 'suite' THEN RAISE EXCEPTION 'nested suites are not allowed' USING ERRCODE = '23514'; END IF;
   INSERT INTO configuration_suite_members (suite_id, configuration_id) VALUES (NEW.id, member_id);
  END LOOP;
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER configuration_suite_members_sync AFTER INSERT OR UPDATE OF definition ON experiment_configurations
 FOR EACH ROW EXECUTE FUNCTION sync_configuration_suite_members();
