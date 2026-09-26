package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const (
	DeadlineCapabilityApplicationTTL = "APPLICATION_TTL_ONLY"
	MaxApplicationTTLSeconds         = uint64(86_400)
)

var (
	ErrInvalidWatchedWallet = errors.New("invalid watched wallet")
	ErrInvalidStrategy      = errors.New("invalid strategy")
	ErrStrategyConflict     = errors.New("strategy version conflict")
	ErrInvalidFeedEvent     = errors.New("invalid feed event")
	ErrFeedGap              = errors.New("feed offset gap")
)

type WatchedWallet struct {
	ID      string
	ChainID uint64
	Address common.Address
	Enabled bool
}

func (w WatchedWallet) Validate() error {
	if strings.TrimSpace(w.ID) == "" || w.ChainID != 4663 || w.Address == (common.Address{}) {
		return ErrInvalidWatchedWallet
	}
	return nil
}

type StrategyConfig struct {
	CopyBuys              bool   `json:"copy_buys"`
	CopySells             bool   `json:"copy_sells"`
	RequireConfirmed      bool   `json:"require_confirmed"`
	FixedBuyAmount        string `json:"fixed_buy_amount,omitempty"`
	MaxBuyAmount          string `json:"max_buy_amount,omitempty"`
	SellBPS               uint16 `json:"sell_bps,omitempty"`
	ApplicationTTLSeconds uint64 `json:"application_ttl_seconds"`
}

func (c StrategyConfig) Validate() error {
	if c.ApplicationTTLSeconds == 0 || c.ApplicationTTLSeconds > MaxApplicationTTLSeconds {
		return ErrInvalidStrategy
	}
	if !c.CopyBuys && !c.CopySells {
		return ErrInvalidStrategy
	}
	if c.CopyBuys {
		fixed, fixedOK := positiveInteger(c.FixedBuyAmount)
		maximum, maxOK := positiveInteger(c.MaxBuyAmount)
		if !fixedOK || !maxOK || fixed.Cmp(maximum) > 0 {
			return ErrInvalidStrategy
		}
	}
	if c.CopySells && (c.SellBPS == 0 || c.SellBPS > 10_000) {
		return ErrInvalidStrategy
	}
	return nil
}

type Strategy struct {
	ID              string
	WatchedWalletID string
	Version         uint64
	Enabled         bool
	Config          StrategyConfig
}

func (s Strategy) Validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.WatchedWalletID) == "" || s.Version == 0 {
		return ErrInvalidStrategy
	}
	return s.Config.Validate()
}

type OperationIntent struct {
	ID                  string         `json:"id"`
	IdempotencyKey      string         `json:"idempotency_key"`
	StrategyID          string         `json:"strategy_id"`
	WatchedWalletID     string         `json:"watched_wallet_id"`
	SourceEventID       string         `json:"source_event_id"`
	SourceObservationID string         `json:"source_observation_id"`
	SourceTxHash        string         `json:"source_tx_hash"`
	Kind                string         `json:"kind"`
	Token               string         `json:"token"`
	AmountMode          string         `json:"amount_mode"`
	AmountValue         string         `json:"amount_value"`
	PolicyVersion       uint64         `json:"policy_version"`
	Policy              StrategyConfig `json:"policy"`
	DeadlineCapability  string         `json:"deadline_capability"`
	ExpiresAt           string         `json:"expires_at"`
	Status              string         `json:"status"`
}

func canonicalExpiry(now time.Time, ttlSeconds uint64) (string, bool) {
	if now.IsZero() || ttlSeconds == 0 || ttlSeconds > MaxApplicationTTLSeconds {
		return "", false
	}
	return now.UTC().Add(time.Duration(ttlSeconds) * time.Second).Format(time.RFC3339Nano), true
}

func deterministicIntentID(strategy Strategy, observationID, kind string) string {
	h := sha256.New()
	for _, part := range []string{strategy.ID, observationID, kind, new(big.Int).SetUint64(strategy.Version).String()} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func encodeConfig(config StrategyConfig) (string, error) {
	value, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func positiveInteger(value string) (*big.Int, bool) {
	parsed, ok := new(big.Int).SetString(value, 10)
	return parsed, ok && parsed.Sign() > 0 && parsed.String() == value
}
