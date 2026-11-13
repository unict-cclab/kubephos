package engine

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/telegram"
	"kubephos.dev/kubephos/internal/testutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelegramMessageLinksAndScope(t *testing.T) {
	d := storage.TelegramDelivery{Event: "completed", ExperimentID: "exp_test", Name: "Research", Kind: "instance"}
	text, link := telegramMessage(d, "http://192.168.0.30:8080/")
	if !strings.Contains(text, "completed successfully") || link != "http://192.168.0.30:8080/#results/instance/exp_test" {
		t.Fatal(text, link)
	}
	d.Event = "failed"
	d.Kind = "suite"
	text, link = telegramMessage(d, "https://kubephos.example")
	if !strings.Contains(text, "failed") || !strings.HasSuffix(link, "#results/suite/exp_test") {
		t.Fatal(text, link)
	}
	for _, root := range []string{"", "javascript:alert(1)", "file:///etc/passwd"} {
		_, link = telegramMessage(d, root)
		if link != "" {
			t.Fatal("unsafe notification link", link)
		}
	}
	d.Event = "test"
	text, link = telegramMessage(d, "https://kubephos.example")
	if link != "" || strings.Contains(text, "Research") {
		t.Fatal("test included experiment details")
	}
}

func TestTelegramSenderDeliversQueuedNotificationAndPersistsOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uri := testutil.Database(t)
	store, err := storage.Open(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pool, err := pgxpool.New(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES('alice','alice','user')`); err != nil {
		t.Fatal(err)
	}
	vault, err := secrets.Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	token := "12345:abcdefghijklmnopqrstuvwxyz"
	nonce, ciphertext, err := vault.EncryptBound([]byte(token), []byte("telegram-integration:12345"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTelegramIntegration(ctx, storage.TelegramIntegration{BotID: 12345, Username: "KubePhosTestBot", Enabled: true, Nonce: nonce, Ciphertext: ciphertext}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.BeginTelegramPairing(ctx, "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	store.ClaimTelegramPolling(ctx, "poller")
	if bound, err := store.ApplyTelegramUpdate(ctx, "poller", 12345, 1, "hash", 101, "Alice"); err != nil || !bound {
		t.Fatal("pairing", err)
	}
	if err = store.QueueTelegramTest(ctx, "alice", "message-test"); err != nil {
		t.Fatal(err)
	}
	sent := make(chan struct{}, 1)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/sendMessage" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		sent <- struct{}{}
	}))
	defer fake.Close()
	worker := &Worker{store: store, vault: vault, telegramClient: telegram.Client{BaseURL: fake.URL, HTTP: fake.Client()}}
	done := make(chan struct{})
	go func() { defer close(done); worker.sendTelegram(ctx) }()
	select {
	case <-sent:
	case <-ctx.Done():
		t.Fatal("notification not delivered")
	}
	for {
		var state string
		if err = pool.QueryRow(ctx, `SELECT status FROM telegram_outbox WHERE id='message-test'`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "sent" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("outcome not persisted")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
