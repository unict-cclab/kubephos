package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var tokenPattern = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,160}$`)
var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)

func ValidToken(token string) bool   { return tokenPattern.MatchString(token) }
func ValidUsername(name string) bool { return usernamePattern.MatchString(name) }

type Client struct {
	BaseURL string
	HTTP    *http.Client
}
type APIError struct {
	Code       int
	RetryAfter int
}

func (e *APIError) Error() string {
	switch e.Code {
	case 401:
		return "Telegram rejected the bot token."
	case 403:
		return "Telegram messages are blocked. Open the bot in Telegram and allow messages."
	case 409:
		return "This bot is already used by another integration."
	case 429:
		return "Telegram is temporarily limiting messages."
	default:
		return "Telegram could not complete the request."
	}
}

type Bot struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}
type Update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		From struct {
			ID        int64  `json:"id"`
			IsBot     bool   `json:"is_bot"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

func (c Client) call(ctx context.Context, token, method string, body any, out any) error {
	if !ValidToken(token) {
		return errors.New("The Telegram bot token is invalid.")
	}
	root := c.BaseURL
	if root == "" {
		root = "https://api.telegram.org"
	}
	data, err := json.Marshal(body)
	if err != nil {
		return errors.New("Could not prepare the Telegram request.")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(root, "/")+"/bot"+token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("Could not prepare the Telegram request.")
	}
	request.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Telegram is unreachable. Try again later.")
	}
	defer response.Body.Close()
	var result struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return errors.New("Telegram returned an invalid response.")
	}
	if !result.OK {
		code := result.Code
		if code == 0 {
			code = response.StatusCode
		}
		return &APIError{Code: code, RetryAfter: result.Parameters.RetryAfter}
	}
	if out != nil && json.Unmarshal(result.Result, out) != nil {
		return errors.New("Telegram returned an invalid response.")
	}
	return nil
}
func (c Client) Validate(ctx context.Context, token string) (Bot, error) {
	var bot Bot
	if err := c.call(ctx, token, "getMe", map[string]any{}, &bot); err != nil {
		return bot, err
	}
	if !bot.IsBot || bot.ID <= 0 || !ValidUsername(bot.Username) {
		return bot, errors.New("The Telegram bot identity is invalid.")
	}
	var webhook struct {
		URL string `json:"url"`
	}
	if err := c.call(ctx, token, "getWebhookInfo", map[string]any{}, &webhook); err != nil {
		return bot, err
	}
	if webhook.URL != "" {
		return bot, errors.New("This bot already has a webhook. Use a dedicated bot for this KubePhos instance.")
	}
	return bot, nil
}
func (c Client) Updates(ctx context.Context, token string, offset int64) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, token, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "limit": 50, "allowed_updates": []string{"message"}}, &updates)
	return updates, err
}
func (c Client) Send(ctx context.Context, token string, chatID int64, text, link string) error {
	body := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if link != "" {
		body["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "Open experiment", "url": link}}}}
	}
	return c.call(ctx, token, "sendMessage", body, nil)
}
func Pairing(update Update, username string) (string, int64, string) {
	m := update.Message
	if m == nil || m.Chat.Type != "private" || m.Chat.ID != m.From.ID || m.From.IsBot {
		return "", 0, ""
	}
	words := strings.Fields(m.Text)
	if len(words) != 2 || (words[0] != "/start" && !strings.EqualFold(words[0], "/start@"+username)) {
		return "", 0, ""
	}
	if len(words[1]) != 43 {
		return "", 0, ""
	}
	name := m.From.Username
	if name == "" {
		name = m.From.FirstName
	}
	return words[1], m.Chat.ID, fmt.Sprintf("%.100s", name)
}
