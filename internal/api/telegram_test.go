package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/telegram"
)

func TestTelegramAdministrationAndPersonalIsolation(t *testing.T) {
	s, pool := transferServer(t, "")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES('admin','admin','admin'),('alice','alice','user'),('bob','bob','user')`); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getMe") {
			w.Write([]byte(`{"ok":true,"result":{"id":12345,"is_bot":true,"username":"KubePhosTestBot"}}`))
		} else {
			w.Write([]byte(`{"ok":true,"result":{"url":""}}`))
		}
	}))
	defer fake.Close()
	s.telegramClient = telegram.Client{BaseURL: fake.URL, HTTP: fake.Client()}
	call := func(handler http.HandlerFunc, user, role, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: user, Role: role}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	token := "12345:abcdefghijklmnopqrstuvwxyz_123456789"
	for _, handler := range []http.HandlerFunc{s.telegramStatus, s.saveTelegram, s.deleteTelegram} {
		if w := call(handler, "alice", "user", `{}`); w.Code != 403 {
			t.Fatalf("non-admin access: %d", w.Code)
		}
	}
	w := call(s.saveTelegram, "admin", "admin", `{"token":"`+token+`","enabled":true}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "ciphertext") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	b, err := s.store.TelegramIntegration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.vault.DecryptBound(b.Nonce, b.Ciphertext, []byte("telegram-integration:12345"))
	if err != nil || string(raw) != token {
		t.Fatal("token not encrypted with bot identity", err)
	}
	w = call(s.connectTelegram, "alice", "user", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pairing: %d %s", w.Code, w.Body.String())
	}
	var link struct {
		URL string `json:"url"`
	}
	json.Unmarshal(w.Body.Bytes(), &link)
	parsed, _ := url.Parse(link.URL)
	code := parsed.Query().Get("start")
	if parsed.Host != "t.me" || len(code) != 43 {
		t.Fatal("invalid pairing link", link.URL)
	}
	var hash string
	var seconds float64
	if err := pool.QueryRow(ctx, `SELECT pairing_hash,extract(epoch from pairing_expires_at-now()) FROM telegram_preferences WHERE user_id='alice'`).Scan(&hash, &seconds); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(code))
	if hash != hex.EncodeToString(sum[:]) || seconds < 590 || seconds > 601 {
		t.Fatal("pairing not hashed or wrong expiry")
	}
	if _, err = s.store.ClaimTelegramPolling(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if bound, err := s.store.ApplyTelegramUpdate(ctx, "test", 12345, 1, hash, 101, "Alice"); err != nil || !bound {
		t.Fatal("pair failed", err)
	}
	w = call(s.telegramPreferences, "alice", "user", "")
	if !strings.Contains(w.Body.String(), `"connected":true`) || strings.Contains(w.Body.String(), "101") || strings.Contains(w.Body.String(), hash) {
		t.Fatal("private details exposed", w.Body.String())
	}
	w = call(s.telegramPreferences, "bob", "user", "")
	if strings.Contains(w.Body.String(), "Alice") || !strings.Contains(w.Body.String(), `"connected":false`) {
		t.Fatal("cross-user preferences", w.Body.String())
	}
	// User-supplied identity is ignored/rejected; it cannot modify another account.
	call(s.updateTelegramPreferences, "bob", "user", `{"enabled":false,"completed":false,"failed":false,"userId":"alice"}`)
	p, err := s.store.TelegramPreferences(ctx, "alice")
	if err != nil || !p.Enabled {
		t.Fatal("other user's settings changed", err)
	}
	w = call(s.testTelegram, "alice", "user", "")
	if w.Code != 202 {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	if w = call(s.testTelegram, "bob", "user", ""); w.Code != 409 {
		t.Fatal("unconnected account queued a test")
	}
	if w = call(s.disconnectTelegram, "alice", "user", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM telegram_outbox`).Scan(&count)
	if count != 0 {
		t.Fatal("disconnect retained queued messages")
	}
}
