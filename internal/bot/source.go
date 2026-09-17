package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
)

type HTTPOutboxSource struct {
	client *http.Client
	auth   *ipc.Authenticator
}

func NewHTTPOutboxSource(socketPath string, auth *ipc.Authenticator) (*HTTPOutboxSource, error) {
	if socketPath == "" || auth == nil {
		return nil, ErrConsumerUnavailable
	}
	return &HTTPOutboxSource{client: ipc.NewUnixClient(socketPath, 5*time.Second), auth: auth}, nil
}

func (s *HTTPOutboxSource) ReadOutboxAfter(ctx context.Context, after int64, limit int) ([]feed.OutboxItem, error) {
	query := url.Values{}
	query.Set("after", strconv.FormatInt(after, 10))
	query.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://feed.internal/internal/feed/outbox?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if err := s.auth.Sign(req, nil); err != nil {
		return nil, err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed outbox status %d", response.StatusCode)
	}
	var decoded struct {
		Items []feed.OutboxItem `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode feed outbox: %w", err)
	}
	return decoded.Items, nil
}
