package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"kubephos.dev/kubephos/internal/catalog"
)

func (s *Store) ListCatalogModels(ctx context.Context) ([]catalog.Model, error) {
	rows, err := s.pool.Query(ctx, `SELECT definition,origin,enabled,created_at,updated_at FROM catalog_models ORDER BY lower(id),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	models := []catalog.Model{}
	for rows.Next() {
		var model catalog.Model
		var raw []byte
		if err := rows.Scan(&raw, &model.Origin, &model.Enabled, &model.CreatedAt, &model.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &model); err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}
func modelDefinition(model catalog.Model) ([]byte, error) {
	return json.Marshal(map[string]any{"id": model.ID, "url": model.URL, "sha256": model.SHA256, "size": model.Size, "backend": model.Backend, "quantization": model.Quantization})
}

func (s *Store) SaveCatalogModel(ctx context.Context, model catalog.Model, create bool) (catalog.Model, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model, err
	}
	defer tx.Rollback(ctx)
	if err := lockModelCatalog(ctx, tx); err != nil {
		return model, err
	}
	if create {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_models WHERE definition->>'url'=$1)`, model.URL).Scan(&exists); err != nil {
			return model, err
		}
		if exists {
			return model, ErrConflict
		}
	}

	raw, err := modelDefinition(model)
	if err != nil {
		return model, err
	}
	query := `UPDATE catalog_models SET definition=$2,enabled=$3,updated_at=now() WHERE id=$1 RETURNING origin,created_at,updated_at`
	if create {
		query = `INSERT INTO catalog_models(id,definition,enabled,origin) VALUES ($1,$2,$3,'custom') RETURNING origin,created_at,updated_at`
	}
	err = tx.QueryRow(ctx, query, model.ID, raw, model.Enabled).Scan(&model.Origin, &model.CreatedAt, &model.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model, ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return model, ErrConflict
	}
	if err != nil {
		return model, err
	}
	if err := refreshModelApplication(ctx, tx); err != nil {
		return model, err
	}
	return model, tx.Commit(ctx)
}
func (s *Store) ArchiveCatalogModel(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockModelCatalog(ctx, tx); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE catalog_models SET enabled=false,updated_at=now() WHERE id=$1 AND enabled=true`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err := refreshModelApplication(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func lockModelCatalog(ctx context.Context, tx pgx.Tx) error {
	// Serialize edits and descriptor snapshots across API instances.
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(741041)`)
	return err
}
func seedCatalogModels(ctx context.Context, tx pgx.Tx, descriptor json.RawMessage) error {
	models, err := catalog.SeedModels(descriptor)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return nil
	}
	if err := lockModelCatalog(ctx, tx); err != nil {
		return err
	}
	for _, model := range models {
		raw, err := modelDefinition(model)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO catalog_models(id,definition,origin) VALUES ($1,$2,'built-in') ON CONFLICT (id) DO NOTHING`, model.ID, raw); err != nil {
			return err
		}
	}
	return refreshModelApplication(ctx, tx)
}
func refreshModelApplication(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT definition FROM catalog_models WHERE enabled=true ORDER BY lower(id),id`)
	if err != nil {
		return err
	}
	models := []catalog.Model{}
	for rows.Next() {
		var raw []byte
		var model catalog.Model
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &model); err != nil {
			rows.Close()
			return err
		}
		models = append(models, model)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var descriptor json.RawMessage
	err = tx.QueryRow(ctx, `SELECT descriptor FROM catalog_applications WHERE id='dev.kubephos.localai'`).Scan(&descriptor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	updated, digest, err := catalog.WithModels(descriptor, models)
	if err != nil {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE catalog_applications SET descriptor=$1,digest=$2,updated_at=CASE WHEN digest<>$2 THEN now() ELSE updated_at END WHERE id='dev.kubephos.localai'`, updated, digest)
	return err
}

func (s *Store) GetCatalogModel(ctx context.Context, id string) (catalog.Model, error) {
	var model catalog.Model
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT definition,origin,enabled,created_at,updated_at FROM catalog_models WHERE id=$1`, id).Scan(&raw, &model.Origin, &model.Enabled, &model.CreatedAt, &model.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model, ErrNotFound
	}
	if err != nil {
		return model, err
	}
	return model, json.Unmarshal(raw, &model)
}
