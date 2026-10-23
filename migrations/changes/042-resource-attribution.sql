CREATE INDEX audit_events_resource_actor_idx ON audit_events(target_id, sequence) WHERE outcome = 'succeeded';
CREATE INDEX audit_events_resource_path_idx ON audit_events((details->>'path'), sequence) WHERE outcome = 'succeeded' AND target_id = '';
