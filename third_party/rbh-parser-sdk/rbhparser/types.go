package rbhparser

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type EventKind uint8

const (
	EventUnknown EventKind = iota
	EventLaunch
	EventPoolInitialized
	EventLiquidityModified
	EventSwap
	EventCurveBuy
	EventCurveSell
	EventFeePolicy
	EventVenueSwap
	EventGraduation
	EventProtocolUpgrade
	EventFlapSupply
	EventVenueState
)

func (k EventKind) String() string {
	switch k {
	case EventLaunch:
		return "launch"
	case EventPoolInitialized:
		return "pool-initialized"
	case EventLiquidityModified:
		return "liquidity-modified"
	case EventSwap:
		return "swap"
	case EventCurveBuy:
		return "curve-buy"
	case EventCurveSell:
		return "curve-sell"
	case EventFeePolicy:
		return "fee-policy"
	case EventVenueSwap:
		return "venue-swap"
	case EventGraduation:
		return "graduation"
	case EventProtocolUpgrade:
		return "protocol-upgrade"
	case EventFlapSupply:
		return "flap-supply"
	case EventVenueState:
		return "venue-state"
	default:
		return "unknown"
	}
}

type IntentKind uint8

const (
	IntentUnknown IntentKind = iota
	IntentBuy
	IntentSell
	IntentLaunch
	IntentLaunchAndBuy
	IntentFeePolicy
)

// TransactionIntent describes requested, unconfirmed execution decoded from a
// signed transaction. AmountOut is deliberately absent because only execution
// and a receipt can establish an actual fill.
type TransactionIntent struct {
	Kind                         IntentKind
	Protocol                     Protocol
	TransactionHash              common.Hash
	Sender                       common.Address
	Recipient                    common.Address
	Contract                     common.Address
	Token                        common.Address
	Quote                        common.Address
	CurrencyIn                   common.Address
	CurrencyOut                  common.Address
	PoolID                       common.Hash
	PoolKey                      PoolKey
	Venue                        common.Address
	Routes                       []VenueRoute
	AmountIn                     *big.Int
	MaximumAmountIn              *big.Int
	MinimumAmountOut             *big.Int
	MinimumIntermediateAmountOut *big.Int
	RequestedAmountOut           *big.Int
	MaximumFeeBPS                uint32
	TransactionValue             *big.Int
	Deadline                     *big.Int
	PonsLaunch                   *PonsLaunchIntent
	LongLaunch                   *LongLaunchIntent
	FeePolicy                    *FeePolicy
	Confirmed                    bool
}

type PonsSocials struct {
	Twitter   string
	Telegram  string
	Discord   string
	Website   string
	Farcaster string
}

type PonsTokenParams struct {
	Name                string
	Symbol              string
	Logo                string
	Description         string
	Socials             PonsSocials
	CreatorFeeRecipient common.Address
	CreatorTaxBPS       uint16
	BuybackEnabled      bool
	ExpectedEconomics   common.Hash
	Salt                common.Hash
}

// PonsLaunchIntent contains every calldata value required to derive the
// CREATE2 curve/token pair without an RPC read on the opportunity path.
type PonsLaunchIntent struct {
	Params             PonsTokenParams
	LaunchConfigID     *big.Int
	OriginalDeployer   common.Address
	SnipeTaxExemptions []common.Address
}

// LongLaunchIntent contains the unchanged Airlock CreateParams forwarded by
// LongLauncher. PoolInitializerData is sufficient to derive the initial V4
// fee and delegated Doppler hook without an RPC read on the feed hot path.
type LongLaunchIntent struct {
	InitialSupply         *big.Int
	NumTokensToSell       *big.Int
	Numeraire             common.Address
	TokenFactory          common.Address
	TokenFactoryData      []byte
	GovernanceFactory     common.Address
	GovernanceFactoryData []byte
	PoolInitializer       common.Address
	PoolInitializerData   []byte
	LiquidityMigrator     common.Address
	LiquidityMigratorData []byte
	Integrator            common.Address
	Salt                  common.Hash
}

type Event struct {
	Kind     EventKind
	Protocol Protocol
	Data     any
	Log      gethtypes.Log
}

type PoolKey struct {
	Currency0   common.Address
	Currency1   common.Address
	Fee         uint32
	TickSpacing int32
	Hooks       common.Address
}

type PoolRegistration struct {
	PoolID   common.Hash
	Protocol Protocol
	Token    common.Address
	Quote    common.Address
	PoolKey  PoolKey
}

type CurveRegistration struct {
	Curve    common.Address
	Protocol Protocol
	Token    common.Address
	Quote    common.Address
}

type PendingPoolRegistration struct {
	PoolID   common.Hash
	Protocol Protocol
	Token    common.Address
	Quote    common.Address
}

// TokenRegistration attributes tokens traded through non-V4 venues and shared routers.
type TokenRegistration struct {
	Token    common.Address
	Protocol Protocol
	Quote    common.Address
	Venue    common.Address
}

type Launch struct {
	Protocol            Protocol
	Token               common.Address
	Quote               common.Address
	Creator             common.Address
	Curve               common.Address
	PoolID              common.Hash
	Pool                *PoolKey
	Name                string
	Symbol              string
	MetadataURI         string
	TickerKey           common.Hash
	NormalizedTicker    string
	PoolOrHook          common.Address
	PoolInitializer     common.Address
	LaunchSupply        *big.Int
	LaunchConfigID      *big.Int
	GraduationThreshold *big.Int
	TickSpacing         int32
	DeployedAt          uint64
	ReservedUntil       uint64
	Venue               common.Address
	BuyFeeBPS           uint32
	SellFeeBPS          uint32
	FeesVerified        bool
	PreLaunch           bool
	LaunchMode          uint8
	AirdropBPS          uint16
	NeedACF             bool
	AntiSniperTaxType   uint8
	IsProject60Days     bool
	TokenVersion        uint8
	MigratorType        uint8
	DexID               uint8
	LPFeeProfile        uint8
	CurveR              *big.Int
	CurveH              *big.Int
	CurveK              *big.Int
	DexSupplyThreshold  *big.Int
}

// FeePolicy is a protocol fee snapshot proven by a launch or fee-update log.
// Rate/Denominator preserves the contract's native units; BPS rounds upward so
// callers never understate the fee when enforcing a maximum.
type FeePolicy struct {
	PoolID                 common.Hash
	Token                  common.Address
	Rate                   uint32
	Denominator            uint32
	BuyFeeBPS              uint32
	SellFeeBPS             uint32
	BaseFeeBPS             uint32
	AntiSnipeStartTotalBPS uint32
	AntiSnipeStartBPS      uint32
	AntiSnipeWindowSeconds uint32
	EffectiveAt            uint64
}

// VenueRoute is one hop emitted by a shared launchpad router.
type VenueRoute struct {
	Kind        uint8
	TokenIn     common.Address
	TokenOut    common.Address
	Pool        common.Address
	Fee         uint32
	TickSpacing int32
	Hook        common.Address
	HookData    []byte
	Router      common.Address
	PoolID      common.Hash
}

// VenueSwap normalizes confirmed non-V4 launchpad trades for copy trading.
type VenueSwap struct {
	Token     common.Address
	Quote     common.Address
	Venue     common.Address
	Recipient common.Address
	Buy       bool
	AmountIn  *big.Int
	AmountOut *big.Int
	Routes    []VenueRoute
	State     *VenueStateChange
}

type ProtocolUpgrade struct {
	Proxy          common.Address
	Implementation common.Address
}

type Graduation struct {
	Token       common.Address
	Venue       common.Address
	AgentToken  common.Address
	TokenAmount *big.Int
	QuoteAmount *big.Int
}

type FlapSupply struct {
	Token             common.Address
	CirculatingSupply *big.Int
}

// VenueStateChange carries non-V4 reserve state derived directly from logs.
// Absolute changes contain Reserve0/Reserve1 (Mint or Sync). Delta changes
// contain the four swap amounts and must be applied to an existing snapshot.
type VenueStateChange struct {
	Token      common.Address
	Venue      common.Address
	Reserve0   *big.Int
	Reserve1   *big.Int
	Amount0In  *big.Int
	Amount0Out *big.Int
	Amount1In  *big.Int
	Amount1Out *big.Int
	Absolute   bool
}

func (p FeePolicy) BPS() uint32 {
	if p.Denominator == 0 {
		return 0
	}
	return uint32((uint64(p.Rate)*10_000 + uint64(p.Denominator) - 1) / uint64(p.Denominator))
}

type PoolInitialized struct {
	PoolID       common.Hash
	PoolKey      PoolKey
	SqrtPriceX96 *big.Int
	Tick         int32
}

type Swap struct {
	PoolID       common.Hash
	Sender       common.Address
	Amount0      *big.Int
	Amount1      *big.Int
	SqrtPriceX96 *big.Int
	Liquidity    *big.Int
	Tick         int32
	Fee          uint32
	Registration PoolRegistration
}

type LiquidityModified struct {
	PoolID         common.Hash
	Sender         common.Address
	TickLower      int32
	TickUpper      int32
	LiquidityDelta *big.Int
	Salt           common.Hash
}

type CurveTrade struct {
	BuyerOrSeller common.Address
	Recipient     common.Address
	AmountIn      *big.Int
	AmountOut     *big.Int
	Fee           *big.Int
	Tax           *big.Int
}
