package storage

import (
	"context"
	"net/url"
	"strings"
)

// ResourceAttribution describes human actions, independently of lifecycle status updates.
type ResourceAttribution struct {
	CreatedBy  string `json:"createdBy,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
}

func (s *Store) ResourceAttributions(ctx context.Context, base string, ids []string) (map[string]ResourceAttribution, error) {
	paths := make([]string, 0, len(ids)*3)
	for _, id := range ids {
		paths = append(paths, base+"/"+url.PathEscape(id), base+"/"+url.PathEscape(id)+"/clusters", base+"/"+url.PathEscape(id)+"/recreate", base+"/"+url.PathEscape(id)+"/reconfigure")
	}
	rows, err := s.pool.Query(ctx, `SELECT actor, target_id, details->>'method', details->>'path'
 FROM audit_events WHERE outcome='succeeded'
 AND (target_id=ANY($1) OR (target_id='' AND details->>'path'=ANY($2)))
 AND (details->>'path'=$3 OR starts_with(details->>'path', $3 || '/'))
 ORDER BY sequence`, ids, paths, base)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]ResourceAttribution{}
	for rows.Next() {
		var actor, id, method, path string
		if err := rows.Scan(&actor, &id, &method, &path); err != nil {
			return nil, err
		}
		id, create, modify := attributionAction(base, id, method, path)
		if id == "" || actor == "" || actor == "anonymous" {
			continue
		}
		value := result[id]
		if create && value.CreatedBy == "" {
			value.CreatedBy = actor
		}
		if modify {
			value.ModifiedBy = actor
		}
		result[id] = value
	}
	return result, rows.Err()
}

func attributionAction(base, id, method, path string) (string, bool, bool) {
	suffix := strings.TrimPrefix(path, base)
	if suffix == path {
		return "", false, false
	}
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	create := method == "POST" && (suffix == "" || base == "/api/v1/experiments" && suffix == "/start" || base == "/api/v1/experiment-configurations" && len(parts) == 2 && parts[1] == "clone")
	if !create {
		if len(parts) == 0 || parts[0] == "" {
			return "", false, false
		}
		parsed, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", false, false
		}
		id = parsed
	}
	modify := (method == "PUT" || method == "PATCH") && (len(parts) == 1 || base == "/api/v1/users" && len(parts) == 2 && parts[1] == "clusters") || base == "/api/v1/catalog/models" && method == "DELETE" && len(parts) == 1
	modify = modify || (base == "/api/v1/experiment-configurations" || base == "/api/v1/experiments") && method == "PUT" && len(parts) == 2 && parts[1] == "sharing"
	modify = modify || base == "/api/v1/kubernetes-clusters" && method == "POST" && len(parts) == 2 && (parts[1] == "recreate" || parts[1] == "reconfigure")
	return id, create, modify
}
