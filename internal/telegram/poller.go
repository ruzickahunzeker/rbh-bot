package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Poller drives Telegram Bot API Long-Polling (getUpdates + sendMessage) against Router.
// Unauthorized chat_ids fail closed silently over Telegram and are logged locally for operator auditing.
type Poller struct {
	baseURL string
	router  *Router
	log     *slog.Logger
	http    *http.Client
	offset  int64
}

func NewPoller(token string, router *Router, log *slog.Logger) (*Poller, error) {
	token = strings.TrimSpace(token)
	if token == "" || router == nil {
		return nil, errors.New("telegram bot token and router are required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Poller{
		baseURL: "https://api.telegram.org/bot" + token,
		router:  router,
		log:     log,
		http: &http.Client{
			Timeout: 35 * time.Second,
		},
	}, nil
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// PollOnce executes one getUpdates long-poll request and replies to authorized commands.
func (p *Poller) PollOnce(ctx context.Context, pollTimeoutSec int) (int, error) {
	if pollTimeoutSec < 0 {
		pollTimeoutSec = 0
	}
	url := fmt.Sprintf("%s/getUpdates?timeout=%d&offset=%d", p.baseURL, pollTimeoutSec, p.offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, err
	}
	var envelope struct {
		OK          bool       `json:"ok"`
		Result      []tgUpdate `json:"result"`
		Description string     `json:"description"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	if !envelope.OK {
		return 0, fmt.Errorf("telegram getUpdates failed: %s", envelope.Description)
	}
	handled := 0
	for _, upd := range envelope.Result {
		if upd.UpdateID >= p.offset {
			p.offset = upd.UpdateID + 1
		}
		if upd.Message == nil || strings.TrimSpace(upd.Message.Text) == "" {
			continue
		}
		chatID := upd.Message.Chat.ID
		reply, err := p.router.HandleCommand(ctx, chatID, upd.Message.Text)
		if errors.Is(err, ErrUnauthorizedChat) {
			// AT-23: Never leak secrets or internal state to unauthorized chats; log chat_id locally only.
			p.log.Warn("ignored telegram command from unauthorized chat", "chat_id", chatID, "username", upd.Message.Chat.Username)
			continue
		}
		if err != nil {
			reply = "Request failed. Check local service logs."
			p.log.Warn("telegram command error", "chat_id", chatID, "err", err)
		}
		if reply != "" {
			if sendErr := p.SendMessage(ctx, chatID, reply); sendErr != nil {
				p.log.Warn("telegram sendMessage error", "chat_id", chatID, "err", sendErr)
			}
		}
		handled++
	}
	return handled, nil
}

func (p *Poller) SendMessage(ctx context.Context, chatID int64, text string) error {
	body, err := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendMessage status %d", resp.StatusCode)
	}
	return nil
}

// Run runs the continuous Telegram Long-Polling loop until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	for {
		if _, err := p.PollOnce(ctx, 20); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			p.log.Warn("telegram poll error; backing off", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}
