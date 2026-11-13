package storage

import (
	"context"
	"errors"
	"kubephos.dev/kubephos/internal/testutil"
	"testing"
	"time"
)

func TestTelegramPairingOutboxAndUserIsolation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role) VALUES('alice','alice','user'),('bob','bob','user'),('carol','carol','user')`)
	b := TelegramIntegration{BotID: 77, Username: "KubePhosTestBot", Enabled: true, Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext")}
	if err := s.SaveTelegramIntegration(ctx, b); err != nil {
		t.Fatal(err)
	}
	for user, hash := range map[string]string{"alice": "alice-code", "bob": "bob-code", "carol": "carol-code"} {
		if _, err := s.BeginTelegramPairing(ctx, user, hash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimTelegramPolling(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimTelegramPolling(ctx, "second"); !errors.Is(err, ErrNotFound) {
		t.Fatal("two pollers claimed the same bot", err)
	}
	bound, err := s.ApplyTelegramUpdate(ctx, "first", 77, 1, "alice-code", 101, "Alice")
	if err != nil || !bound {
		t.Fatal("pairing failed", bound, err)
	}
	bound, err = s.ApplyTelegramUpdate(ctx, "first", 77, 2, "bob-code", 101, "Bob")
	if err != nil || bound {
		t.Fatal("Telegram account was reassigned to a second user", bound, err)
	}
	bound, err = s.ApplyTelegramUpdate(ctx, "first", 77, 3, "bob-code", 202, "Bob")
	if err != nil || !bound {
		t.Fatal(bound, err)
	}
	exec(`UPDATE telegram_preferences SET pairing_expires_at=now()-interval '1 second' WHERE user_id='carol'`)
	bound, err = s.ApplyTelegramUpdate(ctx, "first", 77, 4, "carol-code", 303, "Carol")
	if err != nil || bound {
		t.Fatal("accepted expired pairing", bound, err)
	}
	bound, err = s.ApplyTelegramUpdate(ctx, "first", 77, 5, "alice-code", 404, "Replay")
	if err != nil || bound {
		t.Fatal("accepted reused pairing", bound, err)
	}
	if _, err = s.ApplyTelegramUpdate(ctx, "second", 77, 6, "", 0, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("polling ownership ignored", err)
	}
	if err = s.UpdateTelegramPreferences(ctx, "bob", false, true, true); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob", "carol"} {
		exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,kind) VALUES($1,'ws_default',$1,'running','','',$2,'instance')`, "exp_"+user, user)
		exec(`UPDATE experiments SET status='succeeded' WHERE id=$1`, "exp_"+user)
		exec(`UPDATE experiments SET status='succeeded' WHERE id=$1`, "exp_"+user)
	}
	var total int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox`).Scan(&total); err != nil || total != 1 {
		t.Fatal("notifications duplicated or sent to other owners", total, err)
	}
	d, err := s.ClaimTelegramDelivery(ctx, "sender1", 77)
	if err != nil || d.UserID != "alice" || d.Chat != 101 || d.Event != "completed" {
		t.Fatal("wrong notification recipient", d, err)
	}
	if _, err = s.ClaimTelegramDelivery(ctx, "sender2", 77); !errors.Is(err, ErrNotFound) {
		t.Fatal("duplicate delivery claim", err)
	}
	if err = s.FinishTelegramDelivery(ctx, d, "Temporary error", time.Minute, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimTelegramDelivery(ctx, "early", 77); !errors.Is(err, ErrNotFound) {
		t.Fatal("retry backoff ignored", err)
	}
	exec(`UPDATE telegram_outbox SET available_at=now()`)
	d, err = s.ClaimTelegramDelivery(ctx, "retry", 77)
	if err != nil || d.Attempts != 2 {
		t.Fatal(d, err)
	}
	if err = s.FinishTelegramDelivery(ctx, d, "", 0, false); err != nil {
		t.Fatal(err)
	}
	if err = s.QueueTelegramTest(ctx, "alice", "test1"); err != nil {
		t.Fatal(err)
	}
	if err = s.QueueTelegramTest(ctx, "alice", "test2"); !errors.Is(err, ErrConflict) {
		t.Fatal("test rate limit ignored", err)
	}
	if err = s.UpdateTelegramPreferences(ctx, "alice", true, false, true); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,kind) VALUES('exp_failed','ws_default','Failure','running','','','alice','suite'); UPDATE experiments SET status='failed' WHERE id='exp_failed'`)
	d, err = s.ClaimTelegramDelivery(ctx, "test", 77)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTelegramDelivery(ctx, d, "", 0, false); err != nil {
		t.Fatal(err)
	}
	d, err = s.ClaimTelegramDelivery(ctx, "failure", 77)
	if err != nil || d.Kind != "suite" || d.Event != "failed" {
		t.Fatal("suite terminal error not queued", d, err)
	}
	// Changing a bot invalidates existing subscriptions and queued deliveries.
	b.BotID = 88
	if err = s.SaveTelegramIntegration(ctx, b); err != nil {
		t.Fatal(err)
	}
	p, err := s.TelegramPreferences(ctx, "alice")
	if err != nil || p.Connected {
		t.Fatal("account connected to replacement bot", p, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox`).Scan(&total); err != nil || total != 0 {
		t.Fatal("old notifications survived bot replacement")
	}
	if err = s.DisconnectTelegram(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteTelegramIntegration(ctx); err != nil {
		t.Fatal(err)
	}
}
