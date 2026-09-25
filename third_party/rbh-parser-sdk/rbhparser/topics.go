package rbhparser

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	topicPoolInitialize    = eventTopic("Initialize(bytes32,address,address,uint24,int24,address,uint160,int24)")
	topicPoolSwap          = eventTopic("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)")
	topicPoolLiquidity     = eventTopic("ModifyLiquidity(bytes32,address,int24,int24,int256,bytes32)")
	topicPonsLaunch        = eventTopic("TokenLaunched(address,address,address,address,uint256,uint256)")
	topicPonsCurveBuy      = eventTopic("CurveBuy(address,address,uint256,uint256,uint256,uint256)")
	topicPonsCurveSell     = eventTopic("CurveSell(address,address,uint256,uint256,uint256,uint256)")
	topicPonsPool          = eventTopic("PoolRegistered(bytes32,address,address,address)")
	topicLongLaunch        = eventTopic("LaunchCreated(address,address,address,address,address,bytes32,uint48,uint48,string)")
	topicO1Launch          = eventTopic("Launched(address,bytes32,address,address,uint256,int24)")
	topicO1FeeConfig       = eventTopic("FeeConfigurationUpdated(uint64,uint16,uint16,uint32)")
	topicPoolsCreated      = eventTopic("TokenCreated(address)")
	topicPAIRPool          = eventTopic("PairPoolCreated(address,address,bytes32,uint256,uint16,uint256,int24,int24,uint160,uint256)")
	topicBagsCreated       = eventTopic("TokenCreated(address,address,address,address,address,bytes32,string,string,string)")
	topicBagsCurveBuy      = eventTopic("TokensBought(address,address,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)")
	topicBagsCurveSell     = eventTopic("TokensSold(address,address,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)")
	topicLetsCashLaunch    = common.HexToHash("0x17091df68f499cf4e20dcfc5d42f064dd22359e785b77691c4c4ed0322608897")
	topicLetsCashFee       = common.HexToHash("0xcf68bf26a7137fa3b518ef8d8f79d5fac32a0e2b406aea8ca0636e24c811ad0b")
	topicFlapLaunch        = common.HexToHash("0x504e7f360b2e5fe33cbaaae4c593bc55305328341bf79009e43e0e3b7f699603")
	topicFlapCurve         = eventTopic("TokenCurveSetV2(address,uint256,uint256,uint256)")
	topicFlapDexThreshold  = eventTopic("TokenDexSupplyThreshSet(address,uint256)")
	topicFlapVersion       = eventTopic("TokenVersionSet(address,uint8)")
	topicFlapQuote         = eventTopic("TokenQuoteSet(address,address)")
	topicFlapMigrator      = eventTopic("TokenMigratorSet(address,uint8)")
	topicFlapDexPreference = eventTopic("TokenDexPreferenceSet(address,uint8,uint8)")
	topicFlapTax           = eventTopic("FlapTokenTaxSet(address,uint256)")
	topicFlapAsymmetricTax = eventTopic("FlapTokenAsymmetricTaxSet(address,uint256,uint256)")
	topicFlapSupply        = eventTopic("FlapTokenCirculatingSupplyChanged(address,uint256)")
	topicFlapGraduated     = eventTopic("LaunchedToDEX(address,address,uint256,uint256)")
	topicFlapPoolState     = eventTopic("PoolStateChanged(uint8,uint8)")
	topicFlapVenue         = topicFlapQuote // Backward-compatible alias.
	topicVaroLaunch        = common.HexToHash("0x422c4b44369e863c705352bbe29bc8e926fe8c9edb738712e00c57025adcb675")
	topicVaroFee           = eventTopic("LaunchProtocolFeeUpdated(address,address,uint16,uint16)")
	topicVirtualsPreLaunch = common.HexToHash("0xb9ee8aa6d909a3efd0bf1b0bc2bde7f998f7ad30178b0d45f9227f5382cebc8f")
	topicVirtualsLaunched  = common.HexToHash("0x6ed5dc54f1333f448f2cdf7a6efc675343f880035d6f647fb7f6e9cbf8959718")
	topicVirtualsTax       = common.HexToHash("0x8da1f77a22734510b762a9625e69e737d7c0cc48984e810e5802fb341eb80a3e")
	topicVirtualsGraduated = eventTopic("Graduated(address,address)")
	topicVirtualsPairMade  = eventTopic("PairCreated(address,address,address,uint256)")
	topicProtocolUpgraded  = eventTopic("Upgraded(address)")
	topicGMGNSwap          = common.HexToHash("0x8619026a40d38bedb4002fe511cea4bc4a9b336710efe8f21a61869a7ee0f02a")
	topicVaroBuy           = common.HexToHash("0x0df0b46ad0dfd2d3bee79737b21056819a9b1cc6e97c7d985883949a052b59258")
	topicVirtualsBuy       = common.HexToHash("0x298c349c742327269dc8de6ad66687767310c948ea309df826f5bd103e19d207")
	topicVirtualsPairMint  = eventTopic("Mint(uint256,uint256)")
	topicVirtualsPairSwap  = eventTopic("Swap(uint256,uint256,uint256,uint256)")
	topicVirtualsPairSync  = eventTopic("Sync(uint256,uint256)")
	topicV2PairSync        = eventTopic("Sync(uint112,uint112)")

	TopicPoolInitialize    = topicPoolInitialize
	TopicPoolSwap          = topicPoolSwap
	TopicPoolLiquidity     = topicPoolLiquidity
	TopicPonsLaunch        = topicPonsLaunch
	TopicPonsCurveBuy      = topicPonsCurveBuy
	TopicPonsCurveSell     = topicPonsCurveSell
	TopicPonsPool          = topicPonsPool
	TopicLongLaunch        = topicLongLaunch
	TopicO1Launch          = topicO1Launch
	TopicO1FeeConfig       = topicO1FeeConfig
	TopicPoolsCreated      = topicPoolsCreated
	TopicPAIRPool          = topicPAIRPool
	TopicBagsCreated       = topicBagsCreated
	TopicBagsCurveBuy      = topicBagsCurveBuy
	TopicBagsCurveSell     = topicBagsCurveSell
	TopicLetsCashLaunch    = topicLetsCashLaunch
	TopicLetsCashFee       = topicLetsCashFee
	TopicFlapLaunch        = topicFlapLaunch
	TopicFlapVenue         = topicFlapVenue
	TopicFlapTax           = topicFlapTax
	TopicFlapCurve         = topicFlapCurve
	TopicFlapDexThreshold  = topicFlapDexThreshold
	TopicFlapVersion       = topicFlapVersion
	TopicFlapQuote         = topicFlapQuote
	TopicFlapMigrator      = topicFlapMigrator
	TopicFlapDexPreference = topicFlapDexPreference
	TopicFlapAsymmetricTax = topicFlapAsymmetricTax
	TopicFlapSupply        = topicFlapSupply
	TopicFlapGraduated     = topicFlapGraduated
	TopicFlapPoolState     = topicFlapPoolState
	TopicVaroLaunch        = topicVaroLaunch
	TopicVaroFee           = topicVaroFee
	TopicVirtualsLaunch    = topicVirtualsPreLaunch // Backward-compatible alias for PreLaunched.
	TopicVirtualsPreLaunch = topicVirtualsPreLaunch
	TopicVirtualsLaunched  = topicVirtualsLaunched
	TopicVirtualsTax       = topicVirtualsTax
	TopicVirtualsGraduated = topicVirtualsGraduated
	TopicVirtualsPairMade  = topicVirtualsPairMade
	TopicProtocolUpgraded  = topicProtocolUpgraded
	TopicGMGNSwap          = topicGMGNSwap
	TopicVaroBuy           = topicVaroBuy
	TopicVirtualsBuy       = topicVirtualsBuy
	TopicVirtualsPairMint  = topicVirtualsPairMint
	TopicVirtualsPairSwap  = topicVirtualsPairSwap
	TopicVirtualsPairSync  = topicVirtualsPairSync
	TopicV2PairSync        = topicV2PairSync
)

const longEventABIJSON = `[{"type":"event","name":"LaunchCreated","anonymous":false,"inputs":[{"name":"poolOrHook","type":"address","indexed":true},{"name":"asset","type":"address","indexed":true},{"name":"numeraire","type":"address","indexed":true},{"name":"poolInitializer","type":"address","indexed":false},{"name":"launcher","type":"address","indexed":false},{"name":"tickerKey","type":"bytes32","indexed":false},{"name":"deployedAt","type":"uint48","indexed":false},{"name":"reservedUntil","type":"uint48","indexed":false},{"name":"normalizedTicker","type":"string","indexed":false}]}]`
const bagsEventABIJSON = `[{"type":"event","name":"TokenCreated","anonymous":false,"inputs":[{"name":"token","type":"address","indexed":true},{"name":"curve","type":"address","indexed":true},{"name":"creator","type":"address","indexed":true},{"name":"feeShare","type":"address","indexed":false},{"name":"partner","type":"address","indexed":false},{"name":"poolId","type":"bytes32","indexed":false},{"name":"name","type":"string","indexed":false},{"name":"symbol","type":"string","indexed":false},{"name":"metadataURI","type":"string","indexed":false}]}]`
const flapEventABIJSON = `[{"type":"event","name":"TokenCreated","anonymous":false,"inputs":[{"name":"ts","type":"uint256","indexed":false},{"name":"creator","type":"address","indexed":false},{"name":"nonce","type":"uint256","indexed":false},{"name":"token","type":"address","indexed":false},{"name":"name","type":"string","indexed":false},{"name":"symbol","type":"string","indexed":false},{"name":"meta","type":"string","indexed":false}]}]`

var (
	longEventABI = mustABI(longEventABIJSON)
	bagsEventABI = mustABI(bagsEventABIJSON)
	flapEventABI = mustABI(flapEventABIJSON)
)

func eventTopic(signature string) common.Hash { return crypto.Keccak256Hash([]byte(signature)) }

func mustABI(raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return parsed
}
