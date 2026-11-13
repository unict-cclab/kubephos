package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/telegram"
)

func (s *Server) telegramStatus(w http.ResponseWriter, r *http.Request) {
	if requestUser(r).Role != "admin" {
		writeError(w, 403, "permission_denied", "Administrator access is required.")
		return
	}
	b, err := s.store.TelegramIntegration(r.Context())
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, 200, map[string]any{"configured": false})
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not load Telegram settings.")
		return
	}
	writeJSON(w, 200, map[string]any{"configured": true, "enabled": b.Enabled, "username": b.Username, "lastError": b.LastError})
}
func (s *Server) saveTelegram(w http.ResponseWriter, r *http.Request) {
	if requestUser(r).Role != "admin" {
		writeError(w, 403, "permission_denied", "Administrator access is required.")
		return
	}
	var input struct {
		Token   string `json:"token"`
		Enabled *bool  `json:"enabled"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "invalid_request", "Telegram settings are invalid.")
		return
	}
	if s.vault == nil {
		writeError(w, 503, "unavailable", "Encrypted credential storage is unavailable.")
		return
	}
	token := strings.TrimSpace(input.Token)
	var b storage.TelegramIntegration
	if token == "" {
		var err error
		b, err = s.store.TelegramIntegration(r.Context())
		if err != nil {
			writeError(w, 422, "invalid_token", "Enter a bot token.")
			return
		}
	} else {
		if !telegram.ValidToken(token) {
			writeError(w, 422, "invalid_token", "Enter a valid Telegram bot token.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		bot, err := s.telegramClient.Validate(ctx, token)
		if err != nil {
			writeError(w, 422, "telegram_unavailable", err.Error())
			return
		}
		b.BotID = bot.ID
		b.Username = bot.Username
		b.Nonce, b.Ciphertext, err = s.vault.EncryptBound([]byte(token), []byte("telegram-integration:"+strconv.FormatInt(bot.ID, 10)))
		if err != nil {
			writeError(w, 500, "encryption_failed", "Could not protect the bot token.")
			return
		}
	}
	b.Enabled = true
	if input.Enabled != nil {
		b.Enabled = *input.Enabled
	}
	if err := s.store.SaveTelegramIntegration(r.Context(), b); err != nil {
		writeError(w, 500, "database_error", "Could not save Telegram settings.")
		return
	}
	s.telegramStatus(w, r)
}
func (s *Server) deleteTelegram(w http.ResponseWriter, r *http.Request) {
	if requestUser(r).Role != "admin" {
		writeError(w, 403, "permission_denied", "Administrator access is required.")
		return
	}
	if err := s.store.DeleteTelegramIntegration(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not remove Telegram settings.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) telegramPreferences(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.TelegramPreferences(r.Context(), requestUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not load notification preferences.")
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) updateTelegramPreferences(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled   bool `json:"enabled"`
		Completed bool `json:"completed"`
		Failed    bool `json:"failed"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "invalid_request", "Notification preferences are invalid.")
		return
	}
	if err := s.store.UpdateTelegramPreferences(r.Context(), requestUser(r).ID, input.Enabled, input.Completed, input.Failed); err != nil {
		writeError(w, 500, "database_error", "Could not save notification preferences.")
		return
	}
	s.telegramPreferences(w, r)
}
func (s *Server) connectTelegram(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, 500, "link_failed", "Could not create a Telegram connection.")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	username, err := s.store.BeginTelegramPairing(r.Context(), requestUser(r).ID, hex.EncodeToString(hash[:]))
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "link_unavailable", "Enable the Telegram bot and disconnect any existing account first.")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not create a Telegram connection.")
		return
	}
	if !telegram.ValidUsername(username) {
		writeError(w, 409, "invalid_bot", "Telegram is not configured correctly.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"url": "https://t.me/" + username + "?start=" + token})
}
func (s *Server) disconnectTelegram(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DisconnectTelegram(r.Context(), requestUser(r).ID); err != nil {
		writeError(w, 500, "database_error", "Could not disconnect Telegram.")
		return
	}
	s.telegramPreferences(w, r)
}
func (s *Server) testTelegram(w http.ResponseWriter, r *http.Request) {
	if err := s.store.QueueTelegramTest(r.Context(), requestUser(r).ID, id.New("tg")); errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "test_unavailable", "Connect and enable Telegram first. Wait 30 seconds between tests.")
		return
	} else if err != nil {
		writeError(w, 500, "database_error", "Could not queue the test notification.")
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}
