ALTER TABLE experiment_configurations ADD COLUMN shared_with_everyone boolean NOT NULL DEFAULT false;
ALTER TABLE experiments ADD COLUMN shared_with_everyone boolean NOT NULL DEFAULT false;
CREATE TABLE configuration_shares (
 configuration_id text NOT NULL REFERENCES experiment_configurations(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(configuration_id,user_id)
);
CREATE INDEX configuration_shares_user_idx ON configuration_shares(user_id,configuration_id);
CREATE TABLE experiment_shares (
 experiment_id text NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(experiment_id,user_id)
);
CREATE INDEX experiment_shares_user_idx ON experiment_shares(user_id,experiment_id);
CREATE FUNCTION can_view_experiment(viewer text, resource text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM experiments e WHERE e.id=resource AND
 (e.owner_user_id=viewer OR e.shared_with_everyone OR EXISTS(SELECT 1 FROM experiment_shares s WHERE s.experiment_id=e.id AND s.user_id=viewer)))
$$;
CREATE FUNCTION can_view_configuration(viewer text, resource text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM experiment_configurations c WHERE c.id=resource AND
 (c.owner_user_id=viewer OR c.shared_with_everyone
 OR EXISTS(SELECT 1 FROM configuration_shares s WHERE s.configuration_id=c.id AND s.user_id=viewer)
 OR EXISTS(SELECT 1 FROM experiments e WHERE can_view_experiment(viewer,e.id) AND
   (e.configuration_id=c.id OR EXISTS(SELECT 1 FROM experiment_variants v WHERE v.experiment_id=e.id AND v.configuration_id=c.id)))
 OR EXISTS(SELECT 1 FROM experiment_configurations parent
   CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(parent.definition->'variants')='array' THEN parent.definition->'variants' ELSE '[]'::jsonb END) member
   WHERE member->>'configurationId'=c.id AND (parent.owner_user_id=viewer OR parent.shared_with_everyone
     OR EXISTS(SELECT 1 FROM configuration_shares s WHERE s.configuration_id=parent.id AND s.user_id=viewer)
     OR EXISTS(SELECT 1 FROM experiments e WHERE e.configuration_id=parent.id AND can_view_experiment(viewer,e.id))))))
$$;
CREATE FUNCTION can_view_run(viewer text, resource text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM pipeline_runs r WHERE r.id=resource AND (r.owner_user_id=viewer OR EXISTS(
 SELECT 1 FROM experiment_trials t JOIN experiment_variants v ON v.id=t.variant_id WHERE t.pipeline_run_id=r.id AND can_view_experiment(viewer,v.experiment_id))))
$$;
CREATE FUNCTION can_view_operation(viewer text, resource text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM pipeline_run_stages s WHERE (s.operation_id=resource OR s.cleanup_operation_id=resource) AND can_view_run(viewer,s.run_id))
 OR EXISTS(SELECT 1 FROM experiment_trials t JOIN experiment_variants v ON v.id=t.variant_id WHERE t.operation_id=resource AND can_view_experiment(viewer,v.experiment_id))
$$;
