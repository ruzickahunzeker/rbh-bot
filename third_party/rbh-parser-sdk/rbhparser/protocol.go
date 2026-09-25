package rbhparser

import (
	"fmt"
	"strings"
)

type Protocol uint8

const (
	ProtocolUnknown Protocol = iota
	ProtocolPonsV2
	ProtocolLong
	ProtocolO1
	ProtocolPoolsTrade
	ProtocolPAIR
	ProtocolBagsV2
	ProtocolLetsCash
	ProtocolFlapTax
	ProtocolFlapStocks
	ProtocolVaro
	ProtocolVirtuals
)

func (p Protocol) Valid() bool { return p >= ProtocolPonsV2 && p <= ProtocolVirtuals }

func (p Protocol) String() string {
	switch p {
	case ProtocolPonsV2:
		return "pons-v2"
	case ProtocolLong:
		return "long"
	case ProtocolO1:
		return "o1"
	case ProtocolPoolsTrade:
		return "pools.trade"
	case ProtocolPAIR:
		return "pair"
	case ProtocolBagsV2:
		return "bags-v2"
	case ProtocolLetsCash:
		return "letscash"
	case ProtocolFlapTax:
		return "flap-tax"
	case ProtocolFlapStocks:
		return "flap-stocks"
	case ProtocolVaro:
		return "varo"
	case ProtocolVirtuals:
		return "virtuals"
	default:
		return "unknown"
	}
}

// ProtocolCapabilities describes which protocol-specific facts the parser can
// establish without an RPC lookup. It is deliberately separate from trading
// support: discovering a launch does not imply that its output address or hook
// pricing can be predicted before execution.
type ProtocolCapabilities struct {
	ConfirmedLaunch      bool
	PendingLaunchIntent  bool
	CurveTrades          bool
	PendingCurveIntents  bool
	RegistersV4Pools     bool
	PendingV4SwapIntents bool
	PendingDirectIntents bool
	FeePolicyEvents      bool
	RegistersV2Pairs     bool
	RegistersV3Pools     bool
}

type ProtocolInfo struct {
	Protocol     Protocol
	Capabilities ProtocolCapabilities
}

var supportedProtocols = [...]ProtocolInfo{
	{Protocol: ProtocolPonsV2, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, CurveTrades: true, PendingCurveIntents: true, RegistersV4Pools: true, PendingV4SwapIntents: true}},
	{Protocol: ProtocolLong, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, RegistersV4Pools: true, PendingV4SwapIntents: true}},
	{Protocol: ProtocolO1, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, RegistersV4Pools: true, PendingV4SwapIntents: true}},
	{Protocol: ProtocolPoolsTrade, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, RegistersV4Pools: true, PendingV4SwapIntents: true}},
	{Protocol: ProtocolPAIR, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, RegistersV4Pools: true, PendingV4SwapIntents: true}},
	{Protocol: ProtocolBagsV2, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, CurveTrades: true, PendingCurveIntents: true, RegistersV4Pools: true, PendingV4SwapIntents: true}},
	{Protocol: ProtocolLetsCash, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, RegistersV4Pools: true, PendingV4SwapIntents: true, PendingDirectIntents: true, FeePolicyEvents: true}},
	{Protocol: ProtocolFlapTax, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingDirectIntents: true, FeePolicyEvents: true, RegistersV2Pairs: true, RegistersV3Pools: true}},
	{Protocol: ProtocolFlapStocks, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingDirectIntents: true, FeePolicyEvents: true, RegistersV2Pairs: true}},
	{Protocol: ProtocolVaro, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingDirectIntents: true, FeePolicyEvents: true, RegistersV3Pools: true}},
	{Protocol: ProtocolVirtuals, Capabilities: ProtocolCapabilities{ConfirmedLaunch: true, PendingLaunchIntent: true, PendingDirectIntents: true, FeePolicyEvents: true, RegistersV2Pairs: true}},
}

func SupportedProtocols() []ProtocolInfo {
	out := make([]ProtocolInfo, len(supportedProtocols))
	copy(out, supportedProtocols[:])
	return out
}

func ParseProtocol(value string) (Protocol, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "bankr" || value == "long.xyz" {
		return ProtocolLong, nil
	}
	for _, info := range supportedProtocols {
		if info.Protocol.String() == value {
			return info.Protocol, nil
		}
	}
	return ProtocolUnknown, fmt.Errorf("unsupported protocol %q", value)
}
