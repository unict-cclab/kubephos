package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type TelegramIntegration struct {
	BotID      int64  `json:"botId"`
	Username   string `json:"username"`
	Enabled    bool   `json:"enabled"`
	Nonce      []byte `json:"-"`
	Ciphertext []byte `json:"-"`
	Offset     int64  `json:"-"`
	LastError  string `json:"lastError,omitempty"`
}

func (s *Store) TelegramIntegration(ctx context.Context) (TelegramIntegration, error) {
	var b TelegramIntegration
	err := s.pool.QueryRow(ctx, `SELECT bot_id,username,enabled,token_nonce,token_ciphertext,update_offset,last_error FROM telegram_integration WHERE id=1`).Scan(&b.BotID, &b.Username, &b.Enabled, &b.Nonce, &b.Ciphertext, &b.Offset, &b.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return b, err
}
func (s *Store) SaveTelegramIntegration(ctx context.Context, b TelegramIntegration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('kubephos-telegram-settings'))`); err != nil {
		return err
	}
	var previous int64
	err = tx.QueryRow(ctx, `SELECT bot_id FROM telegram_integration WHERE id=1 FOR UPDATE`).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if previous != b.BotID {
		if _, err = tx.Exec(ctx, `UPDATE telegram_preferences SET chat_id=NULL,telegram_name='',pairing_hash=NULL,pairing_expires_at=NULL,updated_at=now(); DELETE FROM telegram_outbox;`); err != nil {
			return err
		}
	}
	if !b.Enabled {
		if _, err = tx.Exec(ctx, `DELETE FROM telegram_outbox WHERE status IN ('pending','sending'); UPDATE telegram_preferences SET pairing_hash=NULL,pairing_expires_at=NULL`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO telegram_integration(id,bot_id,username,enabled,token_nonce,token_ciphertext) VALUES(1,$1,$2,$3,$4,$5)
 ON CONFLICT(id) DO UPDATE SET bot_id=excluded.bot_id,username=excluded.username,enabled=excluded.enabled,
 token_nonce=excluded.token_nonce,token_ciphertext=excluded.token_ciphertext,
 update_offset=CASE WHEN telegram_integration.bot_id=excluded.bot_id THEN telegram_integration.update_offset ELSE 0 END,
 lease_owner='',lease_until=NULL,last_error='',updated_at=now()`, b.BotID, b.Username, b.Enabled, b.Nonce, b.Ciphertext)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) DeleteTelegramIntegration(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('kubephos-telegram-settings'));DELETE FROM telegram_integration;
 UPDATE telegram_preferences SET chat_id=NULL,telegram_name='',pairing_hash=NULL,pairing_expires_at=NULL,updated_at=now();
 DELETE FROM telegram_outbox;`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type TelegramPreferences struct {
	LastError    string     `json:"lastError,omitempty"`
	Available    bool       `json:"available"`
	Connected    bool       `json:"connected"`
	Enabled      bool       `json:"enabled"`
	Completed    bool       `json:"completed"`
	Failed       bool       `json:"failed"`
	Name         string     `json:"name,omitempty"`
	PendingUntil *time.Time `json:"pendingUntil,omitempty"`
}

func (s *Store) TelegramPreferences(ctx context.Context, user string) (TelegramPreferences, error) {
	p := TelegramPreferences{Enabled: true, Completed: true, Failed: true}
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_integration WHERE enabled)`).Scan(&p.Available); err != nil {
		return p, err
	}
	err := s.pool.QueryRow(ctx, `SELECT chat_id IS NOT NULL,enabled,completed,failed,telegram_name,
 CASE WHEN pairing_expires_at>now() THEN pairing_expires_at END FROM telegram_preferences WHERE user_id=$1`, user).Scan(&p.Connected, &p.Enabled, &p.Completed, &p.Failed, &p.Name, &p.PendingUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = s.pool.QueryRow(ctx, `SELECT last_error FROM telegram_outbox WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, user).Scan(&p.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return p, err
}
func (s *Store) UpdateTelegramPreferences(ctx context.Context, user string, enabled, completed, failed bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO telegram_preferences(user_id,enabled,completed,failed) VALUES($1,$2,$3,$4)
 ON CONFLICT(user_id) DO UPDATE SET enabled=$2,completed=$3,failed=$4,updated_at=now()`, user, enabled, completed, failed); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM telegram_outbox WHERE user_id=$1 AND status IN ('pending','sending') AND
 (NOT $2 OR (event='completed' AND NOT $3) OR (event='failed' AND NOT $4))`, user, enabled, completed, failed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) BeginTelegramPairing(ctx context.Context, user, hash string) (string, error) {
	var username string
	err := s.pool.QueryRow(ctx, `WITH bot AS(SELECT username FROM telegram_integration WHERE enabled FOR SHARE),
 pairing AS(INSERT INTO telegram_preferences(user_id,pairing_hash,pairing_expires_at)
 SELECT $1,$2,now()+interval '10 minutes' FROM bot
 ON CONFLICT(user_id) DO UPDATE SET pairing_hash=excluded.pairing_hash,pairing_expires_at=excluded.pairing_expires_at,updated_at=now()
 WHERE telegram_preferences.chat_id IS NULL RETURNING user_id)
 SELECT username FROM bot WHERE EXISTS(SELECT 1 FROM pairing)`, user, hash).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrConflict
	}
	return username, err
}
func (s *Store) DisconnectTelegram(ctx context.Context, user string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE telegram_preferences SET chat_id=NULL,telegram_name='',pairing_hash=NULL,pairing_expires_at=NULL,updated_at=now() WHERE user_id=$1`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM telegram_outbox WHERE user_id=$1`, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) QueueTelegramTest(ctx context.Context, user, id string) error {
	result, err := s.pool.Exec(ctx, `WITH permitted AS(UPDATE telegram_preferences SET last_test_at=now()
 WHERE user_id=$1 AND chat_id IS NOT NULL AND enabled AND (last_test_at IS NULL OR last_test_at<now()-interval '30 seconds')
 AND EXISTS(SELECT 1 FROM telegram_integration WHERE enabled) RETURNING user_id)
 INSERT INTO telegram_outbox(id,user_id,event) SELECT $2,user_id,'test' FROM permitted`, user, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) ClaimTelegramPolling(ctx context.Context, owner string) (TelegramIntegration, error) {
	var b TelegramIntegration
	err := s.pool.QueryRow(ctx, `UPDATE telegram_integration SET lease_owner=$1,lease_until=now()+interval '75 seconds'
 WHERE id=1 AND enabled AND (lease_until IS NULL OR lease_until<now() OR lease_owner=$1)
 RETURNING bot_id,username,enabled,token_nonce,token_ciphertext,update_offset,last_error`, owner).Scan(&b.BotID, &b.Username, &b.Enabled, &b.Nonce, &b.Ciphertext, &b.Offset, &b.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return b, err
}
func (s *Store) ApplyTelegramUpdate(ctx context.Context, owner string, botID, offset int64, hash string, chat int64, name string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var previous int64
	err = tx.QueryRow(ctx, `SELECT update_offset FROM telegram_integration WHERE id=1 AND enabled AND bot_id=$1 AND lease_owner=$2 AND lease_until>now() FOR UPDATE`, botID, owner).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrConflict
	}
	if err != nil {
		return false, err
	}
	if offset <= previous {
		return false, tx.Commit(ctx)
	}
	bound := false
	if hash != "" && chat > 0 {
		result, err := tx.Exec(ctx, `UPDATE telegram_preferences p SET chat_id=$2,telegram_name=$3,pairing_hash=NULL,pairing_expires_at=NULL,updated_at=now()
  WHERE pairing_hash=$1 AND pairing_expires_at>now() AND chat_id IS NULL
  AND NOT EXISTS(SELECT 1 FROM telegram_preferences other WHERE other.chat_id=$2 AND other.user_id<>p.user_id)`, hash, chat, name)
		if err != nil {
			return false, err
		}
		bound = result.RowsAffected() == 1
	}
	if _, err = tx.Exec(ctx, `UPDATE telegram_integration SET update_offset=$1,last_error='' WHERE id=1`, offset); err != nil {
		return false, err
	}
	return bound, tx.Commit(ctx)
}
func (s *Store) TelegramPollingError(ctx context.Context, owner, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE telegram_integration SET last_error=$2 WHERE id=1 AND lease_owner=$1`, owner, message)
	return err
}

type TelegramDelivery struct {
	ID, Lease, UserID, Event, ExperimentID, Name, Kind string
	Chat                                               int64
	Attempts                                           int
}

func (s *Store) ClaimTelegramDelivery(ctx context.Context, lease string, botID int64) (TelegramDelivery, error) {
	var d TelegramDelivery
	err := s.pool.QueryRow(ctx, `WITH candidate AS(
 SELECT o.id,p.chat_id FROM telegram_outbox o JOIN telegram_preferences p ON p.user_id=o.user_id JOIN users u ON u.id=p.user_id AND u.enabled
 WHERE o.status IN ('pending','sending') AND o.available_at<=now() AND (o.lease_until IS NULL OR o.lease_until<now())
 AND p.enabled AND p.chat_id IS NOT NULL AND (o.event='test' OR (o.event='completed' AND p.completed) OR (o.event='failed' AND p.failed))
 AND EXISTS(SELECT 1 FROM telegram_integration WHERE enabled AND bot_id=$2)
 ORDER BY o.created_at LIMIT 1 FOR UPDATE OF o SKIP LOCKED),
 claimed AS(UPDATE telegram_outbox o SET status='sending',attempts=attempts+1,lease_id=$1,lease_until=now()+interval '90 seconds'
 FROM candidate c WHERE o.id=c.id RETURNING o.*,c.chat_id)
 SELECT c.id,c.lease_id,c.user_id,c.event,coalesce(c.experiment_id,''),coalesce(e.name,''),coalesce(e.kind,''),c.chat_id,c.attempts
 FROM claimed c LEFT JOIN experiments e ON e.id=c.experiment_id`, lease, botID).Scan(&d.ID, &d.Lease, &d.UserID, &d.Event, &d.ExperimentID, &d.Name, &d.Kind, &d.Chat, &d.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return d, err
}
func (s *Store) FinishTelegramDelivery(ctx context.Context, d TelegramDelivery, message string, retryAfter time.Duration, permanent bool) error {
	state := "sent"
	if message != "" {
		state = "pending"
		if permanent || d.Attempts >= 8 {
			state = "failed"
		}
	}
	_, err := s.pool.Exec(ctx, `UPDATE telegram_outbox SET status=$3,last_error=$4,available_at=now()+$5*interval '1 second',lease_until=NULL,lease_id='',
 finished_at=CASE WHEN $3 IN ('sent','failed') THEN now() ELSE NULL END WHERE id=$1 AND lease_id=$2`,
		d.ID, d.Lease, state, message, int(retryAfter.Seconds()))
	return err
}
func (s *Store) CleanupTelegram(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM telegram_outbox WHERE finished_at<now()-interval '7 days' OR created_at<now()-interval '1 day' AND status IN ('pending','sending');
 UPDATE telegram_preferences SET pairing_hash=NULL,pairing_expires_at=NULL WHERE pairing_expires_at<now();`)
	return err
}

func (s *Store) ReleaseTelegramPolling(ctx context.Context, owner string) error {
	_, err := s.pool.Exec(ctx, `UPDATE telegram_integration SET lease_owner='',lease_until=NULL WHERE lease_owner=$1`, owner)
	return err
}
