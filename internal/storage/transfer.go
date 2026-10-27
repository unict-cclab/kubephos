package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/transfer"
)

// The order follows foreign keys. The run/cluster cycle is restored after
// both records exist; suite membership is rebuilt by its trigger.
var transferTables = []string{"workspaces", "credentials", "provider_connections", "catalog_applications", "catalog_models", "catalog_application_previews", "plugin_packages", "operations", "operation_steps", "artifacts", "pipelines", "pipeline_runs", "pipeline_run_stages", "managed_resources", "managed_resource_dependencies", "strategy_catalog", "infrastructure_resources", "plugin_runtime_profiles", "plugin_runtime_registries", "experiment_configurations", "audit_events"}
var transferRoots = []string{"workspaces", "credentials", "provider_connections", "catalog_applications", "catalog_models", "catalog_application_previews", "plugin_packages", "managed_resources", "managed_resource_dependencies", "strategy_catalog", "infrastructure_resources", "plugin_runtime_profiles", "plugin_runtime_registries", "experiment_configurations"}
var transferReference = regexp.MustCompile(`"((?:op|art|pipe|run)_[a-f0-9]{24})"`)
var ErrTransferBusy = errors.New("finish queued or running experiments and infrastructure operations before transferring state")
var ErrTransferTarget = errors.New("import requires a fresh installation without infrastructure, credentials, configurations or experiments")
var ErrTransferSchema = errors.New("the archive database schema does not match this instance; use matching KubePhos releases")

func transferSchema(ctx context.Context, tx pgx.Tx) (string, error) {
	var raw string
	err := tx.QueryRow(ctx, `SELECT string_agg(id || ':' || coalesce(md5sum,''),',' ORDER BY orderexecuted) FROM databasechangelog`).Scan(&raw)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:]), err
}
func transferIdle(ctx context.Context, tx pgx.Tx) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE status IN ('queued','prechecking','running','verifying')) OR EXISTS(SELECT 1 FROM pipeline_runs WHERE status IN ('queued','running')) OR EXISTS(SELECT 1 FROM experiments WHERE status IN ('queued','running')) OR EXISTS(SELECT 1 FROM plugin_import_jobs WHERE status IN ('queued','inspecting','validating','activating'))`).Scan(&active)
	if err != nil {
		return err
	}
	if active {
		return ErrTransferBusy
	}
	return nil
}
func transferRows(ctx context.Context, tx pgx.Tx, table, where string, args ...any) ([]transfer.Row, error) {
	rows, err := tx.Query(ctx, `SELECT to_jsonb(t) FROM `+pgx.Identifier{table}.Sanitize()+` t `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []transfer.Row{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var row transfer.Row
		if err = json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
func (s *Store) ExportState(ctx context.Context) (*transfer.State, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = transferIdle(ctx, tx); err != nil {
		return nil, err
	}
	state := &transfer.State{Format: 1, CreatedAt: time.Now().UTC(), Tables: map[string][]transfer.Row{}, Credentials: map[string][]byte{}, Objects: map[string][]byte{}}
	state.Schema, err = transferSchema(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, table := range transferTables {
		state.Tables[table] = []transfer.Row{}
	}
	for _, table := range transferRoots {
		where := ""
		if table == "infrastructure_resources" {
			where = `WHERE NOT EXISTS(SELECT 1 FROM experiment_trials trial WHERE trial.operation_id=t.created_by_operation_id) AND NOT EXISTS(SELECT 1 FROM pipeline_run_stages stage JOIN experiment_trials trial ON trial.pipeline_run_id=stage.run_id WHERE stage.operation_id=t.created_by_operation_id OR stage.cleanup_operation_id=t.created_by_operation_id)`
		}
		state.Tables[table], err = transferRows(ctx, tx, table, where)
		if err != nil {
			return nil, err
		}
	}
	// Follow infrastructure provenance and typed artifact references, including
	// external inputs. Never copy unrelated operation history or experiment runs.
	seen := map[string]bool{}
	for {
		pending := map[string][]string{}
		for _, rows := range state.Tables {
			for _, row := range rows {
				raw, _ := json.Marshal(row)
				for _, match := range transferReference.FindAllSubmatch(raw, -1) {
					key := string(match[1])
					if seen[key] {
						continue
					}
					seen[key] = true
					table := map[string]string{"op": "operations", "art": "artifacts", "pipe": "pipelines", "run": "pipeline_runs"}[strings.SplitN(key, "_", 2)[0]]
					pending[table] = append(pending[table], key)
				}
			}
		}
		if len(pending) == 0 {
			break
		}
		for table, ids := range pending {
			if table == "operations" || table == "pipeline_runs" {
				field := "operation_id"
				if table == "pipeline_runs" {
					field = "pipeline_run_id"
				}
				var experiment bool
				query := `SELECT EXISTS(SELECT 1 FROM experiment_trials WHERE ` + field + `=ANY($1))`
				if table == "operations" {
					query += ` OR EXISTS(SELECT 1 FROM pipeline_run_stages s JOIN experiment_trials t ON t.pipeline_run_id=s.run_id WHERE s.operation_id=ANY($1) OR s.cleanup_operation_id=ANY($1))`
				}
				if err = tx.QueryRow(ctx, query, ids).Scan(&experiment); err != nil {
					return nil, err
				}
				if experiment {
					return nil, errors.New("infrastructure references experiment execution data; remove that reference before export")
				}
			}
			rows, e := transferRows(ctx, tx, table, "WHERE id=ANY($1)", ids)
			if e != nil {
				return nil, e
			}
			state.Tables[table] = append(state.Tables[table], rows...)
			if table == "operations" {
				steps, e := transferRows(ctx, tx, "operation_steps", "WHERE operation_id=ANY($1)", ids)
				if e != nil {
					return nil, e
				}
				state.Tables["operation_steps"] = append(state.Tables["operation_steps"], steps...)
				objects, e := transferRows(ctx, tx, "artifacts", "WHERE operation_id=ANY($1)", ids)
				if e != nil {
					return nil, e
				}
				for _, row := range objects {
					key := transfer.String(row, "id")
					if !seen[key] {
						state.Tables["artifacts"] = append(state.Tables["artifacts"], row)
						seen[key] = true
					}
				}
			}
			if table == "pipeline_runs" {
				stages, e := transferRows(ctx, tx, "pipeline_run_stages", "WHERE run_id=ANY($1)", ids)
				if e != nil {
					return nil, e
				}
				state.Tables["pipeline_run_stages"] = append(state.Tables["pipeline_run_stages"], stages...)
			}
		}
	}
	// Retain only resource attribution events, not unrelated audit history.
	ids := []string{}
	paths := []string{}
	for _, table := range transferRoots {
		for _, row := range state.Tables[table] {
			identifier := transfer.String(row, "id")
			if identifier == "" {
				continue
			}
			ids = append(ids, identifier)
			for _, base := range []string{"connections", "credentials", "machine-templates", "infrastructure-services", "kubernetes-clusters", "experiment-configurations", "catalog/applications", "catalog/models", "catalog/strategies", "workspaces"} {
				path := "/api/v1/" + base + "/" + identifier
				paths = append(paths, path, path+"/sharing")
			}
		}
	}
	state.Tables["audit_events"], err = transferRows(ctx, tx, "audit_events", `WHERE outcome='succeeded' AND details->>'method' IN ('POST','PUT','PATCH') AND (target_id=ANY($1) OR details->>'path'=ANY($2)) ORDER BY sequence`, ids, paths)
	if err != nil {
		return nil, err
	}
	for _, row := range state.Tables["audit_events"] {
		delete(row, "sequence")
		var details map[string]json.RawMessage
		_ = json.Unmarshal(row["details"], &details)
		safe := transfer.Row{}
		for _, key := range []string{"method", "path", "status"} {
			if v, ok := details[key]; ok {
				safe[key] = v
			}
		}
		row["details"], _ = json.Marshal(safe)
	}
	for _, table := range []string{"pipeline_runs", "experiment_configurations"} {
		for _, row := range state.Tables[table] {
			transfer.Set(row, "owner_user_id", nil)
			if table == "experiment_configurations" {
				transfer.Set(row, "shared_with_everyone", false)
			}
		}
	}
	return state, tx.Commit(ctx)
}
func transferFresh(ctx context.Context, tx pgx.Tx) error {
	if err := transferIdle(ctx, tx); err != nil {
		return err
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM managed_resources) OR EXISTS(SELECT 1 FROM provider_connections) OR EXISTS(SELECT 1 FROM credentials) OR EXISTS(SELECT 1 FROM experiment_configurations) OR EXISTS(SELECT 1 FROM experiments) OR EXISTS(SELECT 1 FROM operations) OR EXISTS(SELECT 1 FROM pipelines) OR EXISTS(SELECT 1 FROM infrastructure_resources) OR EXISTS(SELECT 1 FROM artifacts) OR EXISTS(SELECT 1 FROM plugin_runtime_profiles) OR EXISTS(SELECT 1 FROM plugin_packages) OR EXISTS(SELECT 1 FROM plugin_import_jobs) OR EXISTS(SELECT 1 FROM catalog_applications WHERE origin<>'built-in') OR EXISTS(SELECT 1 FROM catalog_models WHERE origin='custom')`).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return ErrTransferTarget
	}
	return nil
}
func validateState(state *transfer.State) error {
	if state.Format != 1 || len(state.Tables) != len(transferTables) {
		return errors.New("invalid state archive tables")
	}
	for _, table := range transferTables {
		rows, ok := state.Tables[table]
		if !ok || len(rows) > 100000 {
			return errors.New("invalid state archive tables")
		}
		seen := map[string]bool{}
		for _, row := range rows {
			if row == nil {
				return errors.New("invalid state archive row")
			}
			if identifier := transfer.String(row, "id"); identifier != "" {
				if seen[identifier] {
					return errors.New("duplicate state archive row")
				}
				seen[identifier] = true
			}
			status := transfer.String(row, "status")
			if table == "operations" && (status == "queued" || status == "prechecking" || status == "running" || status == "verifying") || table == "pipeline_runs" && (status == "queued" || status == "running") {
				return errors.New("archive contains unfinished operations")
			}
		}
	}
	return nil
}
func (s *Store) CheckStateImport(ctx context.Context, state *transfer.State) error {
	if err := validateState(state); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	schema, err := transferSchema(ctx, tx)
	if err != nil {
		return err
	}
	if schema != state.Schema {
		return ErrTransferSchema
	}
	return transferFresh(ctx, tx)
}

// ImportState commits metadata only after all objects are durable. Pending
// object cleanup is registered outside the transaction, so a process crash
// cannot leave untracked secret files behind. Success cancels cleanup atomically.
func (s *Store) ImportState(ctx context.Context, state *transfer.State, ownerID string, cleanupKeys []string, writeObjects func() error) (err error) {
	if err = validateState(state); err != nil {
		return err
	}
	// Register crash-safe cleanup before taking a transaction connection, so
	// imports also work with a pool limited to a single connection.
	if _, err = s.pool.Exec(ctx, `INSERT INTO artifact_deletions(storage_key,retry_after) SELECT key,now()+interval '1 hour' FROM unnest($1::text[]) key`, cleanupKeys); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_, _ = s.pool.Exec(cleanup, `UPDATE artifact_deletions SET retry_after=now() WHERE storage_key=ANY($1)`, cleanupKeys)
		}
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	locks := append(append([]string{}, transferTables...), "experiments", "configuration_suite_members", "plugin_import_jobs")
	sort.Strings(locks)
	quoted := []string{}
	for _, table := range locks {
		quoted = append(quoted, pgx.Identifier{table}.Sanitize())
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE `+strings.Join(quoted, ",")+` IN EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("could not lock instance for import: %w", err)
	}
	schema, err := transferSchema(ctx, tx)
	if err != nil {
		return err
	}
	if schema != state.Schema {
		return ErrTransferSchema
	}
	if err = transferFresh(ctx, tx); err != nil {
		return err
	}
	if err = writeObjects(); err != nil {
		return err
	}
	for _, table := range []string{"catalog_application_previews", "catalog_models", "catalog_applications", "workspaces"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+pgx.Identifier{table}.Sanitize()); err != nil {
			return err
		}
	}
	// Clear the run/cluster cycle; restore it after managed resources exist.
	type patch struct {
		table, id, key string
		value          json.RawMessage
	}
	patches := []patch{}
	for _, row := range state.Tables["pipeline_runs"] {
		for _, key := range []string{"cluster_resource_id"} {
			if string(row[key]) != "null" && len(row[key]) > 0 {
				patches = append(patches, patch{"pipeline_runs", transfer.String(row, "id"), key, row[key]})
				transfer.Set(row, key, nil)
			}
		}
	}
	names := map[string]bool{}
	configs := state.Tables["experiment_configurations"]
	sort.SliceStable(configs, func(i, j int) bool {
		kind := func(row transfer.Row) string {
			var definition struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(row["definition"], &definition)
			return definition.Kind
		}
		if (kind(configs[i]) == "suite") != (kind(configs[j]) == "suite") {
			return kind(configs[j]) == "suite"
		}
		return transfer.String(configs[i], "id") < transfer.String(configs[j], "id")
	})
	for _, row := range configs {
		transfer.Set(row, "owner_user_id", ownerID)
		transfer.Set(row, "shared_with_everyone", false)
		base := transfer.String(row, "name")
		name := base
		for n := 2; names[strings.ToLower(name)]; n++ {
			suffix := fmt.Sprintf(" (imported %d)", n)
			prefix := []rune(base)
			if len(prefix) > 120-len(suffix) {
				prefix = prefix[:120-len(suffix)]
			}
			name = string(prefix) + suffix
		}
		names[strings.ToLower(name)] = true
		transfer.Set(row, "name", name)
	}
	for _, row := range state.Tables["pipeline_runs"] {
		transfer.Set(row, "owner_user_id", ownerID)
	}
	for _, table := range transferTables {
		for _, row := range state.Tables[table] {
			raw, e := json.Marshal(row)
			if e != nil {
				return e
			}
			query := `INSERT INTO ` + pgx.Identifier{table}.Sanitize() + ` SELECT (jsonb_populate_record(NULL::` + pgx.Identifier{table}.Sanitize() + `,$1::jsonb)).*`
			if table == "audit_events" {
				query = `INSERT INTO audit_events(actor,action,target_type,target_id,outcome,details,created_at) SELECT actor,action,target_type,target_id,outcome,details,created_at FROM jsonb_populate_record(NULL::audit_events,$1::jsonb)`
			}
			_, err = tx.Exec(ctx, query, raw)
			if err != nil {
				return fmt.Errorf("invalid or conflicting %s data in archive: %w", table, err)
			}
		}
	}
	for _, p := range patches {
		if _, err = tx.Exec(ctx, `UPDATE `+pgx.Identifier{p.table}.Sanitize()+` SET `+pgx.Identifier{p.key}.Sanitize()+`=$2 WHERE id=$1`, p.id, transfer.String(transfer.Row{"value": p.value}, "value")); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('plugin_packages','sequence'),greatest(coalesce((SELECT max(sequence) FROM plugin_packages),0)+1,1),false)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM artifact_deletions WHERE storage_key=ANY($1)`, cleanupKeys); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
