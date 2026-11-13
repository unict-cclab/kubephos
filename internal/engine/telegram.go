package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/telegram"
)

func (w *Worker) WithApplicationURL(value string) *Worker {
	w.applicationURL = strings.TrimRight(value, "/")
	return w
}
func (w *Worker) telegramToken(b storage.TelegramIntegration) (string, error) {
	if w.vault == nil {
		return "", errors.New("Encrypted credential storage is unavailable.")
	}
	raw, err := w.vault.DecryptBound(b.Nonce, b.Ciphertext, []byte("telegram-integration:"+strconv.FormatInt(b.BotID, 10)))
	if err != nil {
		return "", errors.New("The Telegram bot token could not be decrypted.")
	}
	return string(raw), nil
}
func notificationWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (w *Worker) pollTelegram(ctx context.Context) {
	owner := id.New("telegram")
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.store.ReleaseTelegramPolling(cleanup, owner)
	}()
	for ctx.Err() == nil {
		b, err := w.store.ClaimTelegramPolling(ctx, owner)
		if err != nil {
			if !errors.Is(err, storage.ErrNotFound) && ctx.Err() == nil {
				slog.Error("claim Telegram polling lease", "error", err)
			}
			if !notificationWait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		token, err := w.telegramToken(b)
		if err == nil {
			var updates []telegram.Update
			updates, err = w.telegramClient.Updates(ctx, token, b.Offset)
			if err == nil {
				_ = w.store.TelegramPollingError(ctx, owner, "")
				for _, update := range updates {
					code, chat, name := telegram.Pairing(update, b.Username)
					hash := ""
					if code != "" {
						sum := sha256.Sum256([]byte(code))
						hash = hex.EncodeToString(sum[:])
					}
					bound, applyErr := w.store.ApplyTelegramUpdate(ctx, owner, b.BotID, update.ID+1, hash, chat, name)
					if applyErr != nil {
						err = errors.New("Could not record the Telegram update.")
						break
					}
					if bound {
						replyCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
						_ = w.telegramClient.Send(replyCtx, token, chat, "Telegram is now connected to your KubePhos account.", "")
						cancel()
					}
				}
			}
		}
		if err != nil && ctx.Err() == nil {
			_ = w.store.TelegramPollingError(ctx, owner, err.Error())
			if !notificationWait(ctx, 10*time.Second) {
				return
			}
		}
	}
}
func (w *Worker) sendTelegram(ctx context.Context) {
	lastCleanup := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastCleanup) > time.Minute {
			if err := w.store.CleanupTelegram(ctx); err != nil && ctx.Err() == nil {
				slog.Error("clean Telegram notification history", "error", err)
			}
			lastCleanup = time.Now()
		}
		b, err := w.store.TelegramIntegration(ctx)
		if err != nil || !b.Enabled {
			if !notificationWait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		delivery, err := w.store.ClaimTelegramDelivery(ctx, id.New("delivery"), b.BotID)
		if err != nil {
			if !errors.Is(err, storage.ErrNotFound) && ctx.Err() == nil {
				slog.Error("claim Telegram notification", "error", err)
			}
			if !notificationWait(ctx, time.Second) {
				return
			}
			continue
		}
		token, err := w.telegramToken(b)
		if err == nil {
			text, link := telegramMessage(delivery, w.applicationURL)
			sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = w.telegramClient.Send(sendCtx, token, delivery.Chat, text, link)
			cancel()
		}
		message := ""
		retry := time.Duration(15*(1<<min(delivery.Attempts-1, 6))) * time.Second
		permanent := false
		if err != nil {
			message = err.Error()
			var apiErr *telegram.APIError
			if errors.As(err, &apiErr) {
				permanent = apiErr.Code == 400 || apiErr.Code == 403
				if apiErr.RetryAfter > 0 {
					retry = time.Duration(min(apiErr.RetryAfter, 3600)) * time.Second
				}
			}
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if finishErr := w.store.FinishTelegramDelivery(finishCtx, delivery, message, retry, permanent); finishErr != nil {
			slog.Error("record Telegram delivery", "error", finishErr)
		}
		cancel()
		if !notificationWait(ctx, time.Second) {
			return
		}
	}
}
func telegramMessage(d storage.TelegramDelivery, applicationURL string) (string, string) {
	if d.Event == "test" {
		return "Your KubePhos Telegram notifications are working.", ""
	}
	state := "completed successfully"
	if d.Event == "failed" {
		state = "failed"
	}
	text := fmt.Sprintf("KubePhos\nExperiment “%s” %s.", d.Name, state)
	root, err := url.Parse(applicationURL)
	if err != nil || root.Host == "" || (root.Scheme != "http" && root.Scheme != "https") {
		return text, ""
	}
	kind := "instance"
	if d.Kind == "suite" {
		kind = "suite"
	}
	root.Fragment = "results/" + kind + "/" + url.PathEscape(d.ExperimentID)
	return text, root.String()
}
