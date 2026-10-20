package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Telegram alert channel through a Bot API bot. Optional and off unless the operator sets
// TELEGRAM_BOT_TOKEN (operator configuration, never tenant input, so a tenant admin cannot point it at
// another host). It needs the internet; an air-gapped site simply leaves it unset. TELEGRAM_API_BASE
// may point at a Bot API compatible relay for air-gapped bridges.
//
// The channel target is the numeric chat id of a user, group or channel (negative for groups and
// channels). The bot must be a member of the chat or Telegram refuses the send.

const defaultTelegramBase = "https://api.telegram.org"

var tgChatRe = regexp.MustCompile(`^-?[0-9]{5,20}$`)

// ValidateTelegramChatID checks a channel target: a numeric chat id such as 123456789 (a person)
// or -1001234567890 (a group or channel).
func ValidateTelegramChatID(id string) error {
	if !tgChatRe.MatchString(id) {
		return errors.New("telegram target must be a numeric chat id like 123456789 or -1001234567890")
	}
	return nil
}

// TelegramConfigured reports whether the operator set the bot token.
func TelegramConfigured() bool { return os.Getenv("TELEGRAM_BOT_TOKEN") != "" }

// Telegram sends one alert as a bot message. Any 2xx is success. The token is part of the Bot API URL,
// so errors never include the URL and any token echo in the API's own message is scrubbed.
func (n *Notifier) Telegram(ctx context.Context, chatID, text string) error {
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tok == "" {
		return errors.New("telegram is not configured")
	}
	if err := ValidateTelegramChatID(chatID); err != nil {
		return err
	}
	base := strings.TrimRight(envOr("TELEGRAM_API_BASE", defaultTelegramBase), "/")
	if err := ValidateWebhookURL(base); err != nil {
		return errors.New("TELEGRAM_API_BASE is not a valid http(s) URL")
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id": chatID, "text": clip(strings.TrimSpace(text), 4000), "disable_web_page_preview": true,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/bot"+tok+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hexmon-iot-telegram/1")
	resp, err := safeClient(n.allowLoopback || os.Getenv("TELEGRAM_ALLOW_LOOPBACK") == "1").Do(req)
	if err != nil {
		return errors.New("telegram: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		var e struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Description != "" {
			return fmt.Errorf("telegram: status %d: %s", resp.StatusCode, clip(strings.ReplaceAll(e.Description, tok, "***"), 200))
		}
		return fmt.Errorf("telegram: status %d", resp.StatusCode)
	}
	return nil
}

// TelegramChat is one destination the bot has seen, from getUpdates.
type TelegramChat struct {
	ID    string `json:"id"`
	Type  string `json:"type"`  // private, group, supergroup, channel
	Title string `json:"title"` // group/channel title, or the person's name for private chats
}

// TelegramMessage is one inbound message the bot has seen, from its own getUpdates feed.
// Used for pairing: a tenant proves it owns a chat by sending its one-time code there.
type TelegramMessage struct {
	Chat   TelegramChat
	Text   string
	Author string // sender username or display name, when Telegram provides one
	FromBot bool   // sender is another bot; the poller drops these (no loops, no spoofed commands)
}

// TelegramUpdate is one raw consumed update: the inbox stores it keyed by UpdateID so the
// poller can advance its cursor exactly once per update.
type TelegramUpdate struct {
	UpdateID int64
	Message  TelegramMessage
}

// TelegramMessages returns recent inbound messages (text + chat) from the bot's getUpdates feed.
// NOTE: Telegram forbids getUpdates while a webhook is set. The planned inbound slice must consume
// this same stream (shared poller) instead of setting a webhook, or pairing discovery stops working.
func (n *Notifier) TelegramMessages(ctx context.Context) ([]TelegramMessage, error) {
	ups, err := n.telegramGetUpdates(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	msgs := make([]TelegramMessage, 0, len(ups))
	for _, u := range ups {
		msgs = append(msgs, u.Message)
	}
	return msgs, nil
}

// TelegramGetUpdates long-polls the bot feed from offset (exclusive confirmation semantics:
// passing the last seen update id + 1 confirms everything below it). timeoutSec is the Bot API
// long-poll window; 0 means a plain short poll. Only the leader-elected inbound poller should
// call this with a nonzero offset: confirming updates removes them from every other reader.
func (n *Notifier) TelegramGetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	return n.telegramGetUpdates(ctx, offset, timeoutSec)
}

func (n *Notifier) telegramGetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tok == "" {
		return nil, errors.New("telegram is not configured")
	}
	base := strings.TrimRight(envOr("TELEGRAM_API_BASE", defaultTelegramBase), "/")
	if err := ValidateWebhookURL(base); err != nil {
		return nil, errors.New("TELEGRAM_API_BASE is not a valid http(s) URL")
	}
	url := base + "/bot" + tok + "/getUpdates?limit=100"
	if offset > 0 {
		url += "&offset=" + strconv.FormatInt(offset, 10)
	}
	if timeoutSec > 0 {
		url += "&timeout=" + strconv.Itoa(timeoutSec)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hexmon-iot-telegram/1")
	client := safeClient(n.allowLoopback || os.Getenv("TELEGRAM_ALLOW_LOOPBACK") == "1")
	if timeoutSec > 0 {
		client = safeClientTimeout(n.allowLoopback || os.Getenv("TELEGRAM_ALLOW_LOOPBACK") == "1", time.Duration(timeoutSec+15)*time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("telegram: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("telegram: status %d", resp.StatusCode)
	}
	var out struct {
		Result []struct {
			UpdateID    int64 `json:"update_id"`
			Message    *tgRawMsg `json:"message"`
			ChannelPost *tgRawMsg `json:"channel_post"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, errors.New("telegram: unreadable reply")
	}
	ups := make([]TelegramUpdate, 0, len(out.Result))
	for _, u := range out.Result {
		m := u.Message
		if m == nil {
			m = u.ChannelPost
		}
		if m == nil {
			continue
		}
		c := m.Chat
		title := c.Title
		if title == "" {
			title = strings.TrimSpace(c.FirstName + " " + c.LastName)
		}
		author := m.From.Username
		if author == "" {
			author = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
		}
		ups = append(ups, TelegramUpdate{
			UpdateID: u.UpdateID,
			Message: TelegramMessage{
				Chat:   TelegramChat{ID: strconv.FormatInt(c.ID, 10), Type: c.Type, Title: title},
				Text:   m.Text,
				Author: author,
				FromBot: m.From.IsBot,
			},
		})
	}
	return ups, nil
}

type tgRawChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type tgRawMsg struct {
	Text string    `json:"text"`
	Chat tgRawChat `json:"chat"`
	From tgRawUser `json:"from"`
}

type tgRawUser struct {
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsBot     bool   `json:"is_bot"`
}
