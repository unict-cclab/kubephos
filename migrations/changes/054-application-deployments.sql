-- Persistent namespaces remain reserved until their ownership-checked cleanup succeeds.
CREATE FUNCTION application_namespace_reserved(cluster_id text, namespace_name text, own_run text) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
  SELECT 1 FROM managed_resources resource
  LEFT JOIN pipeline_runs deletion ON deletion.id=resource.deletion_pipeline_run_id
  WHERE resource.kind='application-deployment'
    AND resource.spec->>'clusterResourceId'=cluster_id
    AND resource.spec->'definition'->>'namespace'=namespace_name
    AND (own_run='' OR COALESCE(resource.pipeline_run_id,'')<>own_run)
    AND (own_run='' OR COALESCE(resource.deletion_pipeline_run_id,'')<>own_run)
    AND COALESCE(deletion.status,'')<>'succeeded'
 )
$$;

CREATE FUNCTION guard_application_deployment_namespace() RETURNS trigger AS $$
DECLARE cluster_id text; namespace_name text;
BEGIN
 IF NEW.kind<>'application-deployment' THEN RETURN NEW; END IF;
 cluster_id:=NEW.spec->>'clusterResourceId'; namespace_name:=NEW.spec->'definition'->>'namespace';
 IF COALESCE(cluster_id,'')='' OR COALESCE(namespace_name,'')='' THEN
  RAISE EXCEPTION 'application deployment requires cluster and namespace' USING ERRCODE='23514';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('cluster:'||cluster_id,0));
 IF application_namespace_reserved(cluster_id,namespace_name,COALESCE(NEW.pipeline_run_id,'')) OR EXISTS(
  SELECT 1 FROM pipeline_runs active WHERE active.cluster_resource_id=cluster_id
    AND active.application_namespace=namespace_name AND active.status='running'
    AND active.id<>COALESCE(NEW.pipeline_run_id,'')
 ) THEN RAISE EXCEPTION 'application namespace is in use' USING ERRCODE='23505'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER application_deployments_namespace_guard BEFORE INSERT OR UPDATE OF spec ON managed_resources
FOR EACH ROW EXECUTE FUNCTION guard_application_deployment_namespace();

CREATE FUNCTION guard_pipeline_application_namespace() RETURNS trigger AS $$
BEGIN
 IF NEW.status<>'running' OR NEW.cluster_resource_id IS NULL OR NEW.application_namespace='' THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('cluster:'||NEW.cluster_resource_id,0));
 IF application_namespace_reserved(NEW.cluster_resource_id,NEW.application_namespace,NEW.id) THEN
  RAISE EXCEPTION 'application namespace is reserved by a deployment' USING ERRCODE='23505';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER pipeline_runs_application_namespace_guard BEFORE INSERT OR UPDATE OF status,application_namespace ON pipeline_runs
FOR EACH ROW EXECUTE FUNCTION guard_pipeline_application_namespace();
