CREATE FUNCTION guard_cluster_maintenance() RETURNS trigger AS $$
DECLARE cluster managed_resources%ROWTYPE;
BEGIN
 IF NEW.cluster_resource_id IS NULL OR NEW.status NOT IN ('queued','running') THEN RETURN NEW; END IF;
 SELECT * INTO cluster FROM managed_resources WHERE id=NEW.cluster_resource_id FOR UPDATE;
 IF NOT FOUND OR cluster.deleted_at IS NOT NULL OR cluster.recreation_pipeline_run_id IS NOT NULL OR cluster.deletion_pipeline_run_id IS NOT NULL OR cluster.deletion_operation_id IS NOT NULL THEN
  RAISE EXCEPTION 'cluster is undergoing maintenance or is unavailable' USING ERRCODE='23505';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER pipeline_runs_cluster_maintenance_guard BEFORE INSERT OR UPDATE OF status ON pipeline_runs FOR EACH ROW EXECUTE FUNCTION guard_cluster_maintenance();
