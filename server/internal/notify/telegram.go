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

// TelegramChats lists the chats the bot has seen recently, so an admin can pick a destination
// instead of hunting for a numeric id. It is the bot's own getUpdates feed; only chats that
// messaged the bot (or had it added) appear. The token never leaves the server.
func (n *Notifier) TelegramChats(ctx context.Context) ([]TelegramChat, error) {
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tok == "" {
		return nil, errors.New("telegram is not configured")
	}
	base := strings.TrimRight(envOr("TELEGRAM_API_BASE", defaultTelegramBase), "/")
	if err := ValidateWebhookURL(base); err != nil {
		return nil, errors.New("TELEGRAM_API_BASE is not a valid http(s) URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/bot"+tok+"/getUpdates?limit=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hexmon-iot-telegram/1")
	resp, err := safeClient(n.allowLoopback || os.Getenv("TELEGRAM_ALLOW_LOOPBACK") == "1").Do(req)
	if err != nil {
		return nil, errors.New("telegram: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("telegram: status %d", resp.StatusCode)
	}
	var out struct {
		Result []struct {
			Message *struct {
				Chat tgRawChat `json:"chat"`
			} `json:"message"`
			EditedMessage *struct {
				Chat tgRawChat `json:"chat"`
			} `json:"edited_message"`
			ChannelPost *struct {
				Chat tgRawChat `json:"chat"`
			} `json:"channel_post"`
			MyChatMember *struct {
				Chat tgRawChat `json:"chat"`
			} `json:"my_chat_member"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, errors.New("telegram: unreadable reply")
	}
	seen := map[string]bool{}
	var chats []TelegramChat
	add := func(c *tgRawChat) {
		if c == nil {
			return
		}
		id := strconv.FormatInt(c.ID, 10)
		if seen[id] {
			return
		}
		seen[id] = true
		title := c.Title
		if title == "" {
			title = strings.TrimSpace(c.FirstName + " " + c.LastName)
		}
		chats = append(chats, TelegramChat{ID: id, Type: c.Type, Title: title})
	}
	for _, u := range out.Result {
		if u.Message != nil {
			add(&u.Message.Chat)
		}
		if u.EditedMessage != nil {
			add(&u.EditedMessage.Chat)
		}
		if u.ChannelPost != nil {
			add(&u.ChannelPost.Chat)
		}
		if u.MyChatMember != nil {
			add(&u.MyChatMember.Chat)
		}
	}
	return chats, nil
}

type tgRawChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
