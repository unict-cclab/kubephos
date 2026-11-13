package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "123456789:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestTelegramClientAndSanitizedErrors(t *testing.T) {
	mode := "normal"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("bot request is not POST")
		}
		if mode == "limited" {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"` + testToken + `","parameters":{"retry_after":45}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "getMe") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"KubePhosTestBot"}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "getWebhookInfo") {
			hook := ""
			if mode == "webhook" {
				hook = "https://existing.example.test"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]string{"url": hook}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "getUpdates") {
			var body struct {
				Offset  int64 `json:"offset"`
				Timeout int   `json:"timeout"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Offset != 8 || body.Timeout != 25 {
				t.Error("polling cursor or timeout was lost")
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":8}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	ctx := context.Background()
	if _, err := client.Validate(ctx, testToken); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Updates(ctx, testToken, 8); err != nil {
		t.Fatal(err)
	}
	if err := client.Send(ctx, testToken, 1, "Message", "http://kubephos.test/#results/instance/exp_1"); err != nil {
		t.Fatal(err)
	}
	mode = "webhook"
	if _, err := client.Validate(ctx, testToken); err == nil {
		t.Fatal("overwrote an existing webhook")
	}
	mode = "limited"
	err := client.Send(ctx, testToken, 1, "Message", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 45 || strings.Contains(err.Error(), testToken) {
		t.Fatal("error details or token handling incorrect", err)
	}
	server.Close()
	err = client.Send(ctx, testToken, 1, "Message", "")
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatal("network error exposed bot credentials", err)
	}
}
func TestPrivatePairingOnly(t *testing.T) {
	code := strings.Repeat("a", 43)
	var update Update
	raw := `{"update_id":1,"message":{"text":"/start ` + code + `","from":{"id":42,"username":"alice"},"chat":{"id":42,"type":"private"}}}`
	if json.Unmarshal([]byte(raw), &update) != nil {
		t.Fatal("bad fixture")
	}
	got, chat, name := Pairing(update, "KubePhosTestBot")
	if got != code || chat != 42 || name != "alice" {
		t.Fatal(got, chat, name)
	}
	update.Message.Chat.Type = "group"
	if got, _, _ := Pairing(update, "KubePhosTestBot"); got != "" {
		t.Fatal("group pairing allowed")
	}
	update.Message.Chat.Type = "private"
	update.Message.From.ID = 99
	if got, _, _ := Pairing(update, "KubePhosTestBot"); got != "" {
		t.Fatal("forwarded identity accepted")
	}
}
