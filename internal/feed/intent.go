package feed

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type IntentNormalizer struct {
	parser *parser.Parser
	filter parser.IntentFilter
}

func NewPonsCurveIntentNormalizer(value *parser.Parser) *IntentNormalizer {
	if value == nil {
		value = parser.New()
	}
	filter := parser.IncludeOnlyIntentProtocols(parser.ProtocolPonsV2)
	filter.Predicate = func(intent parser.TransactionIntent) bool {
		// PR-002 is Curve-only. Pons graduated-v4 intents carry a PoolID and are
		// deliberately deferred to the later V4 slice.
		return intent.PoolID == (common.Hash{})
	}
	return &IntentNormalizer{parser: value, filter: filter}
}

func (n *IntentNormalizer) Potential(tx *gethtypes.Transaction) bool {
	return n != nil && n.parser != nil && n.parser.IsPotentialIntentWithFilter(tx, n.filter)
}

func (n *IntentNormalizer) Normalize(tx *gethtypes.Transaction, sender common.Address, source Source, sourceSequence uint64, observedAt time.Time) ([]Observation, error) {
	if n == nil || n.parser == nil || tx == nil {
		return nil, ErrInvalidObservation
	}
	observations := make([]Observation, 0, 2)
	index := 0
	_, err := n.parser.VisitTransactionIntentsFiltered(tx, sender, n.filter, func(intent parser.TransactionIntent) error {
		payload, err := normalizeIntentPayload(tx.Hash(), intent)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal intent observation: %w", err)
		}
		observation := Observation{
			ChainID:          ChainID,
			Source:           source,
			SourceSequence:   sourceSequence,
			TransactionHash:  tx.Hash(),
			StableActionPath: fmt.Sprintf("intent/%03d/%s", index, payload.IntentKind),
			PayloadJSON:      encoded,
			ObservedAt:       observedAt,
		}
		if err := observation.Validate(); err != nil {
			return err
		}
		observations = append(observations, observation)
		index++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse Pons Curve intent: %w", err)
	}
	return observations, nil
}

type normalizedIntentPayload struct {
	SchemaVersion      int                   `json:"schema_version"`
	Type               string                `json:"type"`
	Protocol           string                `json:"protocol"`
	IntentKind         string                `json:"intent_kind"`
	TransactionHash    string                `json:"transaction_hash"`
	Sender             string                `json:"sender"`
	Recipient          string                `json:"recipient,omitempty"`
	Contract           string                `json:"contract,omitempty"`
	Token              string                `json:"token,omitempty"`
	Quote              string                `json:"quote,omitempty"`
	CurrencyIn         string                `json:"currency_in,omitempty"`
	CurrencyOut        string                `json:"currency_out,omitempty"`
	AmountIn           string                `json:"amount_in,omitempty"`
	MaximumAmountIn    string                `json:"maximum_amount_in,omitempty"`
	MinimumAmountOut   string                `json:"minimum_amount_out,omitempty"`
	RequestedAmountOut string                `json:"requested_amount_out,omitempty"`
	TransactionValue   string                `json:"transaction_value,omitempty"`
	Deadline           string                `json:"deadline,omitempty"`
	Confirmed          bool                  `json:"confirmed"`
	PonsLaunch         *normalizedPonsLaunch `json:"pons_launch,omitempty"`
}

type normalizedPonsLaunch struct {
	Name                string   `json:"name"`
	Symbol              string   `json:"symbol"`
	Logo                string   `json:"logo,omitempty"`
	Description         string   `json:"description,omitempty"`
	CreatorFeeRecipient string   `json:"creator_fee_recipient,omitempty"`
	CreatorTaxBPS       uint16   `json:"creator_tax_bps"`
	BuybackEnabled      bool     `json:"buyback_enabled"`
	ExpectedEconomics   string   `json:"expected_economics,omitempty"`
	Salt                string   `json:"salt,omitempty"`
	LaunchConfigID      string   `json:"launch_config_id,omitempty"`
	OriginalDeployer    string   `json:"original_deployer,omitempty"`
	TaxExemptions       []string `json:"tax_exemptions,omitempty"`
}

func normalizeIntentPayload(txHash common.Hash, intent parser.TransactionIntent) (normalizedIntentPayload, error) {
	if intent.Protocol != parser.ProtocolPonsV2 {
		return normalizedIntentPayload{}, fmt.Errorf("unexpected protocol %s", intent.Protocol.String())
	}
	payload := normalizedIntentPayload{
		SchemaVersion:      1,
		Type:               "transaction_intent",
		Protocol:           intent.Protocol.String(),
		IntentKind:         intentKindString(intent.Kind),
		TransactionHash:    txHash.Hex(),
		Sender:             addressText(intent.Sender),
		Recipient:          addressText(intent.Recipient),
		Contract:           addressText(intent.Contract),
		Token:              addressText(intent.Token),
		Quote:              addressText(intent.Quote),
		CurrencyIn:         addressText(intent.CurrencyIn),
		CurrencyOut:        addressText(intent.CurrencyOut),
		AmountIn:           decimalText(intent.AmountIn),
		MaximumAmountIn:    decimalText(intent.MaximumAmountIn),
		MinimumAmountOut:   decimalText(intent.MinimumAmountOut),
		RequestedAmountOut: decimalText(intent.RequestedAmountOut),
		TransactionValue:   decimalText(intent.TransactionValue),
		Deadline:           decimalText(intent.Deadline),
		Confirmed:          intent.Confirmed,
	}
	if payload.IntentKind == "unknown" {
		return normalizedIntentPayload{}, fmt.Errorf("unexpected Pons intent kind %d", intent.Kind)
	}
	if launch := intent.PonsLaunch; launch != nil {
		normalized := &normalizedPonsLaunch{
			Name:                launch.Params.Name,
			Symbol:              launch.Params.Symbol,
			Logo:                launch.Params.Logo,
			Description:         launch.Params.Description,
			CreatorFeeRecipient: addressText(launch.Params.CreatorFeeRecipient),
			CreatorTaxBPS:       launch.Params.CreatorTaxBPS,
			BuybackEnabled:      launch.Params.BuybackEnabled,
			ExpectedEconomics:   hashText(launch.Params.ExpectedEconomics),
			Salt:                hashText(launch.Params.Salt),
			LaunchConfigID:      decimalText(launch.LaunchConfigID),
			OriginalDeployer:    addressText(launch.OriginalDeployer),
		}
		for _, address := range launch.SnipeTaxExemptions {
			normalized.TaxExemptions = append(normalized.TaxExemptions, addressText(address))
		}
		payload.PonsLaunch = normalized
	}
	return payload, nil
}

func intentKindString(kind parser.IntentKind) string {
	switch kind {
	case parser.IntentBuy:
		return "buy"
	case parser.IntentSell:
		return "sell"
	case parser.IntentLaunch:
		return "launch"
	case parser.IntentLaunchAndBuy:
		return "launch-and-buy"
	default:
		return "unknown"
	}
}

func addressText(value common.Address) string {
	if value == (common.Address{}) {
		return ""
	}
	return value.Hex()
}

func hashText(value common.Hash) string {
	if value == (common.Hash{}) {
		return ""
	}
	return value.Hex()
}

func decimalText(value *big.Int) string {
	if value == nil {
		return ""
	}
	return value.String()
}
