package rbhparser

import (
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	commandTypeMask         = byte(0x7f)
	commandV4Swap           = byte(0x10)
	actionSwapExactInSingle = byte(0x06)
	noDeveloperBuy          = byte(0xff)
	abiWordSize             = 32
	maxNestedIntentDepth    = 4
	maxNestedCalls          = 64
	maxPonsNameBytes        = 64
	maxPonsSymbolBytes      = 16
	maxPonsLogoBytes        = 512
	maxPonsDescriptionBytes = 2048
	maxPonsSocialBytes      = 256
)

var (
	selectorExecute               = methodSelector("execute(bytes,bytes[],uint256)")
	selectorExecuteBatch          = [4]byte{0x34, 0xfc, 0xd5, 0xbe} // executeBatch((address,uint256,bytes)[])
	selectorExecuteBatchAuth      = [4]byte{0xb0, 0xda, 0x32, 0x9a} // EIP-7702 batch of (target,value,data,authData)
	selectorTargetCall            = [4]byte{0xc6, 0x31, 0x43, 0x68} // target and dynamic calldata envelope
	selectorTargetCallV2          = [4]byte{0xe1, 0xb7, 0x7d, 0xb5} // target and dynamic calldata envelope
	selectorExtendedTargetCall    = [4]byte{0xf9, 0x55, 0x75, 0x1f} // target, calldata and value followed by an extended static head
	selectorExtendedTargetCallV2  = [4]byte{0x89, 0x94, 0x21, 0x33} // alternate extended target, calldata and value envelope
	selectorPons7702Launch        = [4]byte{0x84, 0xd5, 0xb1, 0x5d} // EIP-7702 Pons launch envelope
	selectorExecuteMode           = [4]byte{0xe9, 0xae, 0x5c, 0x53} // execute(bytes32,bytes)
	selectorAggregate3Value       = [4]byte{0x17, 0x4d, 0xea, 0x71} // aggregate3Value((address,bool,uint256,bytes)[])
	selectorAtomicLaunchV2        = [4]byte{0x1e, 0x53, 0xb2, 0x3e} // executeAtomicLaunchV2(...)
	selectorEmbeddedPonsLaunchV1  = [4]byte{0x08, 0xaf, 0xcd, 0x5c} // self-call wallet envelope with an ABI-aligned Pons launchAndBuy payload
	selectorEmbeddedPonsLaunchV2  = [4]byte{0x3f, 0x70, 0x7e, 0x6b} // alternate self-call wallet envelope with an ABI-aligned Pons launchAndBuy payload
	selectorPonsBuy               = methodSelector("buy(uint256,uint256,address)")
	selectorPonsSell              = methodSelector("sell(uint256,uint256,address)")
	selectorPonsLaunch            = methodSelector("launchToken((string,string,string,string,(string,string,string,string,string),address,uint16,bool,bytes32,bytes32),uint256,address)")
	selectorPonsLaunchExempt      = methodSelector("launchToken((string,string,string,string,(string,string,string,string,string),address,uint16,bool,bytes32,bytes32),uint256,address,address[])")
	selectorPonsLaunchAndBuy      = methodSelector("launchAndBuy((string,string,string,string,(string,string,string,string,string),address,uint16,bool,bytes32,bytes32),uint256,address,uint256,uint256,address,address[])")
	selectorO1Launch              = methodSelector("createLaunch((string,string,string,bytes32,address,uint64,uint64,bool,string[],string[]))")
	selectorO1LaunchAndBuy        = methodSelector("createLaunchAndBuy((string,string,string,bytes32,address,uint64,uint64,bool,string[],string[]),(address,uint256,uint256,bytes))")
	selectorO1SetFeeConfig        = methodSelector("setFeeConfiguration((uint16,uint16,uint32,(bytes32,uint8,address,uint16)[]))")
	selectorBagsCreate            = methodSelector("create(string,string,string,address,address[],uint16[])")
	selectorBagsCreateAndBuy      = methodSelector("createAndBuy(string,string,string,address,address[],uint16[])")
	selectorBagsBuy               = methodSelector("buy(uint256)")
	selectorBagsBuyFor            = methodSelector("buyFor(address,uint256)")
	selectorBagsSell              = methodSelector("sell(uint256,uint256)")
	selectorBagsSellFor           = methodSelector("sellFor(address,uint256,uint256)")
	selectorLongCreate            = methodSelector("create((uint256,uint256,address,address,bytes,address,bytes,address,bytes,address,bytes,address,bytes32))")
	selectorPAIRLaunch            = methodSelector("launchTokenMulti((string,string,string,bytes32,(address,uint16)[],address,address,uint8,uint256,uint256,uint256))")
	selectorPoolsCreateToken      = methodSelector("createToken(address,string,string,uint8,uint128,address,bytes)")
	selectorLetsCashBuy           = [4]byte{0xf8, 0x90, 0xc8, 0x2b}
	selectorVaroBuy               = methodSelector("buy(address,uint256,uint256)")
	selectorVaroSell              = methodSelector("sell(address,address,uint256,uint256,uint256)")
	selectorVaroSetFee            = methodSelector("setLaunchProtocolFee(address,uint16)")
	selectorVirtualsBuy           = [4]byte{0x73, 0x46, 0xbe, 0x70} // native-router buy; five static ABI words
	selectorVirtualsSell          = methodSelector("sell(address,uint256,uint256,uint256,uint256)")
	selectorVirtualsLaunch        = [4]byte{0x21, 0x40, 0x13, 0xca} // launch(address)
	selectorVirtualsSetTax        = [4]byte{0x93, 0x6b, 0x29, 0x34} // setProjectTaxRates(uint16,uint16)
	selectorVirtualsFactorySetTax = methodSelector("setTaxParams(address,uint256,uint256,uint256,address)")
	selectorGMGNSwap              = [4]byte{0x4d, 0x81, 0x9a, 0x2a}
	selectorV2BuyFeeOnTransfer    = methodSelector("swapExactETHForTokensSupportingFeeOnTransferTokens(uint256,address[],address,uint256)")
	selectorV2SellFeeOnTransfer   = methodSelector("swapExactTokensForETHSupportingFeeOnTransferTokens(uint256,uint256,address[],address,uint256)")
)

type IntentVisitor func(TransactionIntent) error

// IsPotentialIntent is intended for blockrazor.Config.Filter. It only inspects
// destination and selector, avoiding signature recovery for unrelated traffic.
func (p *Parser) IsPotentialIntent(tx *gethtypes.Transaction) bool {
	return p.IsPotentialIntentWithFilter(tx, IntentFilter{})
}

// IsPotentialIntentWithFilter applies protocol/kind/contract filters before
// sender recovery and full ABI decoding. Exact sender/token/pool predicates are
// applied by VisitTransactionIntentsFiltered after decoding.
func (p *Parser) IsPotentialIntentWithFilter(tx *gethtypes.Transaction, filter IntentFilter) bool {
	if p == nil || p.registry == nil || tx == nil {
		return false
	}
	data := tx.Data()
	if len(data) < 4 || len(data)-4 > maxLogDataBytes {
		return false
	}
	to := tx.To()
	if to == nil {
		return false
	}
	call := intentCall{transaction: tx, contract: *to, data: data}
	return p.isPotentialCall(call, filter, 0)
}

func (p *Parser) isPotentialCall(call intentCall, filter IntentFilter, depth int) bool {
	if len(call.data) < 4 {
		return false
	}
	selector := [4]byte(call.data[:4])
	if registration, ok := p.registry.LookupCurve(call.contract); ok {
		if !filter.matchContract(call.contract) {
			return false
		}
		switch registration.Protocol {
		case ProtocolPonsV2:
			return (selector == selectorPonsBuy && filter.matchClassification(registration.Protocol, IntentBuy)) ||
				(selector == selectorPonsSell && filter.matchClassification(registration.Protocol, IntentSell))
		case ProtocolBagsV2:
			return ((selector == selectorBagsBuy || selector == selectorBagsBuyFor) && filter.matchClassification(registration.Protocol, IntentBuy)) ||
				((selector == selectorBagsSell || selector == selectorBagsSellFor) && filter.matchClassification(registration.Protocol, IntentSell))
		}
		return false
	}
	if registration, ok := p.registry.LookupToken(call.contract); ok && registration.Protocol == ProtocolVirtuals {
		return selector == selectorVirtualsSetTax && filter.matchClassification(ProtocolVirtuals, IntentFeePolicy)
	}
	switch call.contract {
	case p.addresses.UniversalRouter:
		return filter.matchContract(call.contract) && selector == selectorExecute && filter.mayMatchV4()
	case p.addresses.PonsFactory:
		return filter.matchContract(call.contract) && (selector == selectorPonsLaunch || selector == selectorPonsLaunchExempt) && filter.matchClassification(ProtocolPonsV2, IntentLaunch)
	case p.addresses.PonsLaunchAndBuy:
		return filter.matchContract(call.contract) && selector == selectorPonsLaunchAndBuy && filter.matchClassification(ProtocolPonsV2, IntentLaunchAndBuy)
	case p.addresses.O1Factory:
		return filter.matchContract(call.contract) && ((selector == selectorO1Launch && filter.matchClassification(ProtocolO1, IntentLaunch)) ||
			(selector == selectorO1LaunchAndBuy && filter.matchClassification(ProtocolO1, IntentLaunchAndBuy)) ||
			(selector == selectorO1SetFeeConfig && filter.matchClassification(ProtocolO1, IntentFeePolicy)))
	case p.addresses.BagsFactory:
		return filter.matchContract(call.contract) && ((selector == selectorBagsCreate && filter.matchClassification(ProtocolBagsV2, IntentLaunch)) ||
			(selector == selectorBagsCreateAndBuy && filter.matchClassification(ProtocolBagsV2, IntentLaunchAndBuy)))
	case p.addresses.LongLauncher:
		return filter.matchContract(call.contract) && selector == selectorLongCreate && filter.matchClassification(ProtocolLong, IntentLaunch)
	case p.addresses.PAIRLaunchpad:
		return filter.matchContract(call.contract) && selector == selectorPAIRLaunch && (filter.matchClassification(ProtocolPAIR, IntentLaunch) || filter.matchClassification(ProtocolPAIR, IntentLaunchAndBuy))
	case p.addresses.PoolsEntry:
		return filter.matchContract(call.contract) && selector == selectorPoolsCreateToken && filter.matchClassification(ProtocolPoolsTrade, IntentLaunch)
	case p.addresses.LetsCashRouter:
		return filter.matchContract(call.contract) && selector == selectorLetsCashBuy && filter.matchClassification(ProtocolLetsCash, IntentBuy)
	case p.addresses.VaroRouter:
		return filter.matchContract(call.contract) && ((selector == selectorVaroBuy && filter.matchClassification(ProtocolVaro, IntentBuy)) ||
			(selector == selectorVaroSell && filter.matchClassification(ProtocolVaro, IntentSell)))
	case p.addresses.VaroLaunchpad:
		return filter.matchContract(call.contract) && selector == selectorVaroSetFee && filter.matchClassification(ProtocolVaro, IntentFeePolicy)
	case p.addresses.VirtualsRouter:
		return filter.matchContract(call.contract) && ((selector == selectorVirtualsBuy && filter.matchClassification(ProtocolVirtuals, IntentBuy)) ||
			(selector == selectorVirtualsSell && filter.matchClassification(ProtocolVirtuals, IntentSell)))
	case p.addresses.VirtualsLaunchpad:
		return filter.matchContract(call.contract) && selector == selectorVirtualsLaunch && filter.matchClassification(ProtocolVirtuals, IntentLaunch)
	case p.addresses.VirtualsFactory:
		return filter.matchContract(call.contract) && selector == selectorVirtualsFactorySetTax && filter.matchClassification(ProtocolVirtuals, IntentFeePolicy)
	case p.addresses.GMGNRouter:
		return filter.matchContract(call.contract) && selector == selectorGMGNSwap &&
			(filter.matchClassification(ProtocolFlapTax, IntentBuy) || filter.matchClassification(ProtocolFlapTax, IntentSell) ||
				filter.matchClassification(ProtocolFlapStocks, IntentBuy) || filter.matchClassification(ProtocolFlapStocks, IntentSell))
	case p.addresses.UniswapV2Router:
		return filter.matchContract(call.contract) && ((selector == selectorV2BuyFeeOnTransfer && (filter.matchClassification(ProtocolFlapTax, IntentBuy) || filter.matchClassification(ProtocolFlapStocks, IntentBuy))) ||
			(selector == selectorV2SellFeeOnTransfer && (filter.matchClassification(ProtocolFlapTax, IntentSell) || filter.matchClassification(ProtocolFlapStocks, IntentSell))))
	}
	if depth >= maxNestedIntentDepth {
		return false
	}
	args := call.data[4:]
	switch selector {
	case selectorExecuteBatch, selectorExecuteBatchAuth:
		if call.transaction.Type() != gethtypes.SetCodeTxType {
			return false
		}
		headWords := 3
		if selector == selectorExecuteBatchAuth {
			headWords = 4
		}
		calls, err := newDynamicCallArray(args, headWords)
		if err != nil {
			return false
		}
		for index := 0; index < calls.length; index++ {
			contract, data, err := calls.view(index)
			child := intentCall{transaction: call.transaction, contract: contract, data: data}
			if err == nil && p.isPotentialCall(child, filter, depth+1) {
				return true
			}
		}
	case selectorTargetCall, selectorTargetCallV2, selectorExtendedTargetCall, selectorExtendedTargetCallV2:
		contract, data, err := targetCallView(args)
		child := intentCall{transaction: call.transaction, contract: contract, data: data}
		return err == nil && p.isPotentialCall(child, filter, depth+1)
	case selectorPons7702Launch:
		if call.transaction.Type() != gethtypes.SetCodeTxType {
			return false
		}
		data, err := pons7702CallData(args)
		child := intentCall{transaction: call.transaction, contract: p.addresses.PonsLaunchAndBuy, data: data}
		return err == nil && p.isPotentialCall(child, filter, depth+1)
	case selectorExecuteMode:
		if call.transaction.Type() != gethtypes.SetCodeTxType {
			return false
		}
		calls, err := executeModeCalls(args)
		if err != nil {
			return false
		}
		return p.anyPotentialCall(call, calls, filter, depth)
	case selectorAggregate3Value:
		calls, err := newMulticall3ValueArray(args)
		if err != nil {
			return false
		}
		return p.anyPotentialCall(call, calls, filter, depth)
	case selectorAtomicLaunchV2:
		return filter.matchContract(p.addresses.PonsFactory) && filter.matchClassification(ProtocolPonsV2, IntentLaunch)
	case selectorEmbeddedPonsLaunchV1, selectorEmbeddedPonsLaunchV2:
		return (filter.matchContract(p.addresses.PonsLaunchAndBuy) || filter.matchContract(p.addresses.PonsFactory)) &&
			(filter.matchClassification(ProtocolPonsV2, IntentLaunch) || filter.matchClassification(ProtocolPonsV2, IntentLaunchAndBuy))
	}
	return false
}

func (p *Parser) anyPotentialCall(parent intentCall, calls dynamicCallArray, filter IntentFilter, depth int) bool {
	for index := 0; index < calls.length; index++ {
		contract, data, err := calls.view(index)
		child := intentCall{transaction: parent.transaction, contract: contract, data: data}
		if err == nil && p.isPotentialCall(child, filter, depth+1) {
			return true
		}
	}
	return false
}

func (k IntentKind) String() string {
	switch k {
	case IntentBuy:
		return "buy"
	case IntentSell:
		return "sell"
	case IntentLaunch:
		return "launch"
	case IntentLaunchAndBuy:
		return "launch-and-buy"
	case IntentFeePolicy:
		return "fee-policy"
	default:
		return "unknown"
	}
}

// ParseTransactionIntent returns the first supported intent in tx. Use
// VisitTransactionIntents when a Universal Router transaction may contain
// multiple swaps and allocations on the hot path matter.
func (p *Parser) ParseTransactionIntent(tx *gethtypes.Transaction, sender common.Address) (TransactionIntent, bool, error) {
	return p.ParseTransactionIntentFiltered(tx, sender, IntentFilter{})
}

func (p *Parser) ParseTransactionIntentFiltered(tx *gethtypes.Transaction, sender common.Address, filter IntentFilter) (TransactionIntent, bool, error) {
	var first TransactionIntent
	count, err := p.VisitTransactionIntentsFiltered(tx, sender, filter, func(intent TransactionIntent) error {
		if countZero(first) {
			first = intent
		}
		return nil
	})
	return first, count > 0, err
}

func countZero(intent TransactionIntent) bool {
	return intent.Kind == IntentUnknown
}

// ParseTransactionIntents is the convenient allocating API. Latency-sensitive
// callers should use VisitTransactionIntents to receive intents directly.
func (p *Parser) ParseTransactionIntents(tx *gethtypes.Transaction, sender common.Address) ([]TransactionIntent, error) {
	return p.ParseTransactionIntentsFiltered(tx, sender, IntentFilter{})
}

func (p *Parser) ParseTransactionIntentsFiltered(tx *gethtypes.Transaction, sender common.Address, filter IntentFilter) ([]TransactionIntent, error) {
	intents := make([]TransactionIntent, 0, 1)
	_, err := p.VisitTransactionIntentsFiltered(tx, sender, filter, func(intent TransactionIntent) error {
		intents = append(intents, intent)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return intents, nil
}

// VisitTransactionIntents performs no RPC calls and emits each recognized
// unconfirmed intent synchronously in transaction order.
func (p *Parser) VisitTransactionIntents(tx *gethtypes.Transaction, sender common.Address, visit IntentVisitor) (int, error) {
	return p.VisitTransactionIntentsFiltered(tx, sender, IntentFilter{}, visit)
}

func (p *Parser) VisitTransactionIntentsFiltered(tx *gethtypes.Transaction, sender common.Address, filter IntentFilter, visit IntentVisitor) (int, error) {
	if visit == nil {
		return 0, ErrInvalidRegistration
	}
	delivered := 0
	_, err := p.visitTransactionIntents(tx, sender, func(intent TransactionIntent) error {
		if !filter.Match(intent) {
			return nil
		}
		delivered++
		return visit(intent)
	})
	return delivered, err
}

func (p *Parser) visitTransactionIntents(tx *gethtypes.Transaction, sender common.Address, visit IntentVisitor) (int, error) {
	if p == nil || p.registry == nil || tx == nil || visit == nil || sender == (common.Address{}) {
		return 0, ErrInvalidRegistration
	}
	to := tx.To()
	if to == nil {
		return 0, nil
	}
	call := intentCall{transaction: tx, sender: sender, contract: *to, data: tx.Data(), value: tx.Value()}
	return p.visitCallIntents(call, 0, visit)
}

func (p *Parser) visitCallIntents(call intentCall, depth int, visit IntentVisitor) (int, error) {
	if len(call.data) < 4 {
		return 0, nil
	}
	if len(call.data)-4 > maxLogDataBytes {
		return 0, malformedCalldata("router calldata exceeds maximum size")
	}
	selector := [4]byte(call.data[:4])

	if registration, ok := p.registry.LookupCurve(call.contract); ok {
		intent, recognized, err := parseCurveIntent(registration, call, selector, call.data[4:])
		if err != nil || !recognized {
			return 0, err
		}
		if err := visit(intent); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if registration, ok := p.registry.LookupToken(call.contract); ok && registration.Protocol == ProtocolVirtuals {
		if selector != selectorVirtualsSetTax {
			return 0, nil
		}
		intent, err := parseVirtualsTaxUpdate(call, registration)
		return emitOne(intent, err, visit)
	}

	switch call.contract {
	case p.addresses.UniversalRouter:
		if selector != selectorExecute {
			return 0, nil
		}
		return p.visitV4Intents(call, call.data[4:], visit)
	case p.addresses.PonsFactory:
		switch selector {
		case selectorPonsLaunch:
			intent, err := parsePonsLaunch(call, call.data[4:], false)
			return emitOne(intent, err, visit)
		case selectorPonsLaunchExempt:
			intent, err := parsePonsLaunch(call, call.data[4:], true)
			return emitOne(intent, err, visit)
		}
	case p.addresses.PonsLaunchAndBuy:
		if selector != selectorPonsLaunchAndBuy {
			return 0, nil
		}
		intent, err := parsePonsLaunchAndBuy(call, call.data[4:])
		return emitOne(intent, err, visit)
	case p.addresses.O1Factory:
		switch selector {
		case selectorO1LaunchAndBuy:
			intent, err := parseO1LaunchAndBuy(call, call.data[4:])
			return emitOne(intent, err, visit)
		case selectorO1Launch:
			intent, err := parseO1Launch(call, call.data[4:])
			return emitOne(intent, err, visit)
		case selectorO1SetFeeConfig:
			intent, err := parseO1FeePolicy(call, call.data[4:])
			return emitOne(intent, err, visit)
		}
	case p.addresses.BagsFactory:
		switch selector {
		case selectorBagsCreateAndBuy:
			intent := baseIntent(call, ProtocolBagsV2, IntentLaunchAndBuy)
			return emitOne(intent, nil, visit)
		case selectorBagsCreate:
			return emitOne(baseIntent(call, ProtocolBagsV2, IntentLaunch), nil, visit)
		}
	case p.addresses.LongLauncher:
		if selector == selectorLongCreate {
			intent, err := parseLongCreate(call, call.data[4:])
			return emitOne(intent, err, visit)
		}
	case p.addresses.PAIRLaunchpad:
		if selector == selectorPAIRLaunch {
			intent, err := parsePAIRLaunch(call, call.data[4:])
			return emitOne(intent, err, visit)
		}
	case p.addresses.PoolsEntry:
		if selector == selectorPoolsCreateToken {
			return emitOne(baseIntent(call, ProtocolPoolsTrade, IntentLaunch), nil, visit)
		}
	case p.addresses.LetsCashRouter:
		if selector == selectorLetsCashBuy {
			intent, recognized, err := p.parseLetsCashBuy(call)
			return emitRecognized(intent, recognized, err, visit)
		}
	case p.addresses.VaroRouter:
		intent, recognized, err := p.parseVaroIntent(call, selector)
		return emitRecognized(intent, recognized, err, visit)
	case p.addresses.VaroLaunchpad:
		if selector == selectorVaroSetFee {
			intent, recognized, err := p.parseVaroFeeUpdate(call)
			return emitRecognized(intent, recognized, err, visit)
		}
	case p.addresses.VirtualsRouter:
		intent, recognized, err := p.parseVirtualsIntent(call, selector)
		return emitRecognized(intent, recognized, err, visit)
	case p.addresses.VirtualsLaunchpad:
		if selector == selectorVirtualsLaunch {
			intent, err := p.parseVirtualsLaunch(call)
			return emitOne(intent, err, visit)
		}
	case p.addresses.VirtualsFactory:
		if selector == selectorVirtualsFactorySetTax {
			intent, err := parseVirtualsFactoryTaxUpdate(call)
			return emitOne(intent, err, visit)
		}
	case p.addresses.GMGNRouter:
		if selector == selectorGMGNSwap {
			intent, recognized, err := p.parseGMGNIntent(call)
			return emitRecognized(intent, recognized, err, visit)
		}
	case p.addresses.UniswapV2Router:
		intent, recognized, err := p.parseFlapV2Intent(call, selector)
		return emitRecognized(intent, recognized, err, visit)
	}
	if depth >= maxNestedIntentDepth {
		return 0, nil
	}
	args := call.data[4:]
	switch selector {
	case selectorExecuteBatch, selectorExecuteBatchAuth:
		if call.transaction.Type() != gethtypes.SetCodeTxType || (depth == 0 && call.contract != call.sender) {
			return 0, nil
		}
		headWords := 3
		if selector == selectorExecuteBatchAuth {
			headWords = 4
		}
		calls, err := newDynamicCallArray(args, headWords)
		if err != nil {
			return 0, err
		}
		count := 0
		for index := 0; index < calls.length; index++ {
			child, err := calls.at(call, index)
			if err != nil {
				return count, err
			}
			n, err := p.visitCallIntents(child, depth+1, visit)
			count += n
			if err != nil {
				return count, err
			}
		}
		return count, nil
	case selectorTargetCall, selectorTargetCallV2:
		child, err := targetCall(call, args)
		if err != nil {
			return 0, err
		}
		return p.visitCallIntents(child, depth+1, visit)
	case selectorExtendedTargetCall, selectorExtendedTargetCallV2:
		child, err := targetCall(call, args)
		if err != nil {
			return 0, err
		}
		if selector == selectorExtendedTargetCall {
			child.ponsDeployer = ponsDeployerCreator
		} else {
			child.ponsDeployer = ponsDeployerUnknown
		}
		return p.visitCallIntents(child, depth+1, visit)
	case selectorPons7702Launch:
		if call.transaction.Type() != gethtypes.SetCodeTxType || (depth == 0 && call.contract != call.sender) {
			return 0, nil
		}
		child, err := p.pons7702Call(call, args)
		if err != nil {
			return 0, err
		}
		return p.visitCallIntents(child, depth+1, visit)
	case selectorExecuteMode:
		if call.transaction.Type() != gethtypes.SetCodeTxType || (depth == 0 && call.contract != call.sender) {
			return 0, nil
		}
		calls, err := executeModeCalls(args)
		if err != nil {
			return 0, err
		}
		return p.visitNestedCallArray(call, calls, depth, visit)
	case selectorAggregate3Value:
		calls, err := newMulticall3ValueArray(args)
		if err != nil {
			return 0, err
		}
		return p.visitNestedCallArray(call, calls, depth, visit)
	case selectorAtomicLaunchV2:
		if depth == 0 && call.contract != call.sender {
			return 0, nil
		}
		intent, err := p.parseAtomicLaunchV2(call, args)
		return emitOne(intent, err, visit)
	case selectorEmbeddedPonsLaunchV1, selectorEmbeddedPonsLaunchV2:
		if depth != 0 || call.contract != call.sender {
			return 0, nil
		}
		intent, err := p.parseEmbeddedPonsLaunch(call)
		return emitOne(intent, err, visit)
	}
	return 0, nil
}

func (p *Parser) visitNestedCallArray(parent intentCall, calls dynamicCallArray, depth int, visit IntentVisitor) (int, error) {
	count := 0
	for index := 0; index < calls.length; index++ {
		child, err := calls.at(parent, index)
		if err != nil {
			return count, err
		}
		n, err := p.visitCallIntents(child, depth+1, visit)
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

func emitOne(intent TransactionIntent, err error, visit IntentVisitor) (int, error) {
	if err != nil {
		return 0, err
	}
	if err := visit(intent); err != nil {
		return 0, err
	}
	return 1, nil
}

func emitRecognized(intent TransactionIntent, recognized bool, err error, visit IntentVisitor) (int, error) {
	if err != nil || !recognized {
		return 0, err
	}
	return emitOne(intent, nil, visit)
}

type ponsDeployerMode uint8

const (
	ponsDeployerSender ponsDeployerMode = iota
	ponsDeployerCreator
	ponsDeployerUnknown
)

type intentCall struct {
	transaction  *gethtypes.Transaction
	sender       common.Address
	contract     common.Address
	data         []byte
	value        *big.Int
	ponsDeployer ponsDeployerMode
}

func baseIntent(call intentCall, protocol Protocol, kind IntentKind) TransactionIntent {
	return TransactionIntent{
		Kind:             kind,
		Protocol:         protocol,
		TransactionHash:  call.transaction.Hash(),
		Sender:           call.sender,
		Contract:         call.contract,
		TransactionValue: new(big.Int).Set(call.value),
	}
}

func (p *Parser) parseVirtualsLaunch(call intentCall) (TransactionIntent, error) {
	args := call.data[4:]
	if len(args) != abiWordSize || requireCleanAddresses(args, 0) != nil {
		return TransactionIntent{}, malformedCalldata("invalid Virtuals launch calldata")
	}
	token := readAddress(args, 0)
	registration, ok := p.registry.LookupToken(token)
	if !ok || registration.Protocol != ProtocolVirtuals {
		return TransactionIntent{}, malformedCalldata("unregistered Virtuals launch token")
	}
	intent := baseIntent(call, ProtocolVirtuals, IntentLaunch)
	intent.Token, intent.Quote, intent.Venue = token, registration.Quote, registration.Venue
	return intent, nil
}

func parseVirtualsTaxUpdate(call intentCall, registration TokenRegistration) (TransactionIntent, error) {
	args := call.data[4:]
	if len(args) != 2*abiWordSize {
		return TransactionIntent{}, malformedCalldata("invalid Virtuals tax update length")
	}
	buy, err := uintWord(args, 0, 16)
	if err != nil || !buy.IsUint64() {
		return TransactionIntent{}, malformedCalldata("invalid Virtuals buy tax")
	}
	sell, err := uintWord(args, 1, 16)
	if err != nil || !sell.IsUint64() {
		return TransactionIntent{}, malformedCalldata("invalid Virtuals sell tax")
	}
	policy := &FeePolicy{Token: registration.Token, Rate: uint32(buy.Uint64()), Denominator: 10_000, BuyFeeBPS: uint32(buy.Uint64()), SellFeeBPS: uint32(sell.Uint64())}
	intent := baseIntent(call, ProtocolVirtuals, IntentFeePolicy)
	intent.Token, intent.Quote, intent.Venue, intent.FeePolicy = registration.Token, registration.Quote, registration.Venue, policy
	return intent, nil
}

func parseVirtualsFactoryTaxUpdate(call intentCall) (TransactionIntent, error) {
	args := call.data[4:]
	if len(args) != 5*abiWordSize || requireCleanAddresses(args, 0, 4) != nil {
		return TransactionIntent{}, malformedCalldata("invalid Virtuals factory tax update")
	}
	buy, buyErr := uintWord(args, 1, 256)
	sell, sellErr := uintWord(args, 2, 256)
	anti, antiErr := uintWord(args, 3, 256)
	if buyErr != nil || sellErr != nil || antiErr != nil || !buy.IsUint64() || !sell.IsUint64() || !anti.IsUint64() || buy.Uint64() > 99 || sell.Uint64() > 99 || anti.Uint64() > 99 || buy.Uint64()+anti.Uint64() > 100 {
		return TransactionIntent{}, malformedCalldata("invalid Virtuals factory tax values")
	}
	intent := baseIntent(call, ProtocolVirtuals, IntentFeePolicy)
	intent.FeePolicy = &FeePolicy{
		BuyFeeBPS: uint32(buy.Uint64() * 100), SellFeeBPS: uint32(sell.Uint64() * 100),
		AntiSnipeStartBPS: uint32(anti.Uint64() * 100), Denominator: 10_000,
	}
	return intent, nil
}

func (p *Parser) parseLetsCashBuy(call intentCall) (TransactionIntent, bool, error) {
	const compactBytes = 20 + 6 + 20 + 16 + 2
	args := call.data[4:]
	if len(args) != compactBytes {
		return TransactionIntent{}, true, malformedCalldata("invalid letscash compact buy length")
	}
	token := common.BytesToAddress(args[:20])
	tickSpacing := int64(0)
	for _, value := range args[20:26] {
		tickSpacing = tickSpacing<<8 | int64(value)
	}
	hook := common.BytesToAddress(args[26:46])
	minimumOut := new(big.Int).SetBytes(args[46:62])
	maxFeeBPS := binary.BigEndian.Uint16(args[62:64])
	if token == (common.Address{}) || tickSpacing <= 0 || tickSpacing > 8388607 || hook != p.addresses.LetsCashHook || minimumOut.Sign() <= 0 || maxFeeBPS == 0 || maxFeeBPS >= 10_000 || call.value == nil || call.value.Sign() <= 0 {
		return TransactionIntent{}, true, malformedCalldata("invalid letscash compact buy")
	}
	registration, ok := p.registry.LookupToken(token)
	if !ok || registration.Protocol != ProtocolLetsCash {
		return TransactionIntent{}, false, nil
	}
	key := PoolKey{Currency0: common.Address{}, Currency1: token, TickSpacing: int32(tickSpacing), Hooks: hook}
	id, err := PoolID(key)
	if err != nil {
		return TransactionIntent{}, true, err
	}
	pool, ok := p.registry.LookupPool(id)
	if !ok || pool.Protocol != ProtocolLetsCash || pool.Token != token || pool.PoolKey != key {
		return TransactionIntent{}, false, nil
	}
	intent := baseIntent(call, ProtocolLetsCash, IntentBuy)
	intent.Recipient = call.sender
	intent.Token, intent.Quote = token, pool.Quote
	intent.CurrencyIn, intent.CurrencyOut = pool.Quote, token
	intent.PoolID, intent.PoolKey = id, key
	intent.AmountIn, intent.MinimumAmountOut = new(big.Int).Set(call.value), minimumOut
	return intent, true, nil
}

func (p *Parser) parseVaroIntent(call intentCall, selector [4]byte) (TransactionIntent, bool, error) {
	args := call.data[4:]
	words := 3
	if selector == selectorVaroSell {
		words = 5
	} else if selector != selectorVaroBuy {
		return TransactionIntent{}, false, nil
	}
	if len(args) != words*abiWordSize || requireCleanAddresses(args, 0) != nil {
		return TransactionIntent{}, true, malformedCalldata("invalid Varo router calldata")
	}
	venue := readAddress(args, 0)
	registration, ok := p.registry.LookupVenue(venue)
	if !ok || registration.Protocol != ProtocolVaro {
		return TransactionIntent{}, false, nil
	}
	intent := baseIntent(call, ProtocolVaro, IntentBuy)
	intent.Recipient, intent.Token, intent.Quote, intent.Venue = call.sender, registration.Token, registration.Quote, venue
	intent.CurrencyIn, intent.CurrencyOut = registration.Quote, registration.Token
	if selector == selectorVaroBuy {
		intent.AmountIn, intent.MinimumAmountOut, intent.Deadline = new(big.Int).Set(call.value), readBig(args, 1), readBig(args, 2)
	} else {
		if call.value != nil && call.value.Sign() != 0 {
			return TransactionIntent{}, true, malformedCalldata("Varo sell cannot carry native value")
		}
		if err := requireCleanAddresses(args, 1); err != nil || readAddress(args, 1) != registration.Token {
			return TransactionIntent{}, true, malformedCalldata("invalid Varo sell token")
		}
		intent.Kind = IntentSell
		intent.CurrencyIn, intent.CurrencyOut = registration.Token, registration.Quote
		intent.AmountIn, intent.MinimumAmountOut, intent.Deadline = readBig(args, 2), readBig(args, 3), readBig(args, 4)
	}
	if intent.AmountIn.Sign() <= 0 || intent.MinimumAmountOut.Sign() <= 0 || intent.Deadline.Sign() <= 0 {
		return TransactionIntent{}, true, malformedCalldata("invalid Varo router amounts")
	}
	return intent, true, nil
}

func (p *Parser) parseVaroFeeUpdate(call intentCall) (TransactionIntent, bool, error) {
	args := call.data[4:]
	if len(args) != 2*abiWordSize || requireCleanAddresses(args, 0) != nil {
		return TransactionIntent{}, true, malformedCalldata("invalid Varo fee update calldata")
	}
	token := readAddress(args, 0)
	registration, ok := p.registry.LookupToken(token)
	if !ok || registration.Protocol != ProtocolVaro {
		return TransactionIntent{}, false, nil
	}
	fee, err := uintWord(args, 1, 16)
	if err != nil || !fee.IsUint64() || fee.Uint64() > 3_000 {
		return TransactionIntent{}, true, malformedCalldata("invalid Varo protocol fee")
	}
	feeBPS := uint32(fee.Uint64())
	intent := baseIntent(call, ProtocolVaro, IntentFeePolicy)
	intent.Token, intent.Quote, intent.Venue = token, registration.Quote, registration.Venue
	intent.FeePolicy = &FeePolicy{Token: token, Rate: feeBPS, Denominator: 10_000, BuyFeeBPS: feeBPS, SellFeeBPS: feeBPS}
	return intent, true, nil
}

func (p *Parser) parseVirtualsIntent(call intentCall, selector [4]byte) (TransactionIntent, bool, error) {
	args := call.data[4:]
	if selector != selectorVirtualsBuy && selector != selectorVirtualsSell {
		return TransactionIntent{}, false, nil
	}
	if len(args) != 5*abiWordSize || requireCleanAddresses(args, 0) != nil {
		return TransactionIntent{}, true, malformedCalldata("invalid Virtuals router calldata")
	}
	token := readAddress(args, 0)
	registration, ok := p.registry.LookupToken(token)
	if !ok || registration.Protocol != ProtocolVirtuals {
		return TransactionIntent{}, false, nil
	}
	intent := baseIntent(call, ProtocolVirtuals, IntentBuy)
	intent.Recipient, intent.Token, intent.Quote, intent.Venue = call.sender, token, registration.Quote, registration.Venue
	intent.CurrencyIn, intent.CurrencyOut = registration.Quote, token
	if selector == selectorVirtualsSell {
		if call.value != nil && call.value.Sign() != 0 {
			return TransactionIntent{}, true, malformedCalldata("Virtuals sell cannot carry native value")
		}
		intent.Kind = IntentSell
		intent.CurrencyIn, intent.CurrencyOut = token, registration.Quote
		intent.AmountIn = readBig(args, 1)
		intent.MinimumIntermediateAmountOut = readBig(args, 2)
		intent.MinimumAmountOut = readBig(args, 3)
		intent.Deadline = readBig(args, 4)
	} else {
		intent.MinimumIntermediateAmountOut, intent.MinimumAmountOut = readBig(args, 1), readBig(args, 2)
		intent.AmountIn = new(big.Int).Set(call.value)
		intent.Deadline = readBig(args, 3)
		maxFee := readBig(args, 4)
		if !maxFee.IsUint64() || maxFee.Sign() <= 0 || maxFee.Uint64() >= 10_000 {
			return TransactionIntent{}, true, malformedCalldata("invalid Virtuals maximum fee")
		}
		intent.MaximumFeeBPS = uint32(maxFee.Uint64())
	}
	if call.value == nil || intent.AmountIn.Sign() <= 0 || intent.MinimumIntermediateAmountOut.Sign() <= 0 || intent.MinimumAmountOut.Sign() <= 0 || intent.Deadline.Sign() <= 0 {
		return TransactionIntent{}, true, malformedCalldata("invalid Virtuals router amounts")
	}
	return intent, true, nil
}

func parseCurveIntent(registration CurveRegistration, call intentCall, selector [4]byte, args []byte) (TransactionIntent, bool, error) {
	intent := baseIntent(call, registration.Protocol, IntentUnknown)
	intent.Token = registration.Token
	intent.Quote = registration.Quote
	intent.Recipient = call.sender
	switch registration.Protocol {
	case ProtocolPonsV2:
		switch selector {
		case selectorPonsBuy:
			if err := requireWords(args, 3); err != nil {
				return TransactionIntent{}, true, err
			}
			if err := requireCleanAddresses(args, 2); err != nil {
				return TransactionIntent{}, true, err
			}
			intent.Kind = IntentBuy
			intent.AmountIn = readBig(args, 0)
			intent.MinimumAmountOut = readBig(args, 1)
			intent.Recipient = readAddress(args, 2)
			intent.CurrencyIn, intent.CurrencyOut = registration.Quote, registration.Token
		case selectorPonsSell:
			if err := requireWords(args, 3); err != nil {
				return TransactionIntent{}, true, err
			}
			if err := requireCleanAddresses(args, 2); err != nil {
				return TransactionIntent{}, true, err
			}
			intent.Kind = IntentSell
			intent.AmountIn = readBig(args, 0)
			intent.MinimumAmountOut = readBig(args, 1)
			intent.Recipient = readAddress(args, 2)
			intent.CurrencyIn, intent.CurrencyOut = registration.Token, registration.Quote
		default:
			return TransactionIntent{}, false, nil
		}
	case ProtocolBagsV2:
		switch selector {
		case selectorBagsBuy:
			if err := requireWords(args, 1); err != nil {
				return TransactionIntent{}, true, err
			}
			intent.Kind = IntentBuy
			intent.AmountIn = new(big.Int).Set(call.value)
			intent.MinimumAmountOut = readBig(args, 0)
			intent.CurrencyIn, intent.CurrencyOut = registration.Quote, registration.Token
		case selectorBagsBuyFor:
			if err := requireWords(args, 2); err != nil {
				return TransactionIntent{}, true, err
			}
			if err := requireCleanAddresses(args, 0); err != nil {
				return TransactionIntent{}, true, err
			}
			intent.Kind = IntentBuy
			intent.Recipient = readAddress(args, 0)
			intent.AmountIn = new(big.Int).Set(call.value)
			intent.MinimumAmountOut = readBig(args, 1)
			intent.CurrencyIn, intent.CurrencyOut = registration.Quote, registration.Token
		case selectorBagsSell:
			if err := requireWords(args, 2); err != nil {
				return TransactionIntent{}, true, err
			}
			intent.Kind = IntentSell
			intent.AmountIn = readBig(args, 0)
			intent.MinimumAmountOut = readBig(args, 1)
			intent.CurrencyIn, intent.CurrencyOut = registration.Token, registration.Quote
		case selectorBagsSellFor:
			if err := requireWords(args, 3); err != nil {
				return TransactionIntent{}, true, err
			}
			if err := requireCleanAddresses(args, 0); err != nil {
				return TransactionIntent{}, true, err
			}
			intent.Kind = IntentSell
			intent.Recipient = readAddress(args, 0)
			intent.AmountIn = readBig(args, 1)
			intent.MinimumAmountOut = readBig(args, 2)
			intent.CurrencyIn, intent.CurrencyOut = registration.Token, registration.Quote
		default:
			return TransactionIntent{}, false, nil
		}
	default:
		return TransactionIntent{}, false, nil
	}
	return intent, true, nil
}

func (p *Parser) visitV4Intents(call intentCall, args []byte, visit IntentVisitor) (int, error) {
	if err := requireWords(args, 3); err != nil {
		return 0, err
	}
	commandsOffset, err := readOffset(args, 0)
	if err != nil {
		return 0, err
	}
	inputsOffset, err := readOffset(args, 1)
	if err != nil {
		return 0, err
	}
	commands, err := readDynamicBytes(args, commandsOffset)
	if err != nil {
		return 0, err
	}
	inputs, err := newDynamicBytesArray(args, inputsOffset)
	if err != nil {
		return 0, err
	}
	if len(commands) != inputs.length {
		return 0, malformedCalldata("router command/input length mismatch")
	}
	deadline := readBig(args, 2)
	count := 0
	for i, command := range commands {
		if command&commandTypeMask != commandV4Swap {
			continue
		}
		plan, err := inputs.at(i)
		if err != nil {
			return count, err
		}
		n, err := p.visitV4Plan(call, deadline, plan, visit)
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

func (p *Parser) visitV4Plan(call intentCall, deadline *big.Int, plan []byte, visit IntentVisitor) (int, error) {
	if err := requireWords(plan, 2); err != nil {
		return 0, err
	}
	actionsOffset, err := readOffset(plan, 0)
	if err != nil {
		return 0, err
	}
	paramsOffset, err := readOffset(plan, 1)
	if err != nil {
		return 0, err
	}
	actions, err := readDynamicBytes(plan, actionsOffset)
	if err != nil {
		return 0, err
	}
	params, err := newDynamicBytesArray(plan, paramsOffset)
	if err != nil {
		return 0, err
	}
	if len(actions) != params.length {
		return 0, malformedCalldata("v4 action/parameter length mismatch")
	}
	count := 0
	for i, action := range actions {
		if action != actionSwapExactInSingle {
			continue
		}
		encoded, err := params.at(i)
		if err != nil {
			return count, err
		}
		intent, ok, err := p.parseV4ExactInput(call, deadline, encoded)
		if err != nil {
			return count, err
		}
		if !ok {
			continue
		}
		if err := visit(intent); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (p *Parser) parseV4ExactInput(call intentCall, deadline *big.Int, encoded []byte) (TransactionIntent, bool, error) {
	if err := requireWords(encoded, 1); err != nil {
		return TransactionIntent{}, false, err
	}
	tupleOffset, err := readOffset(encoded, 0)
	if err != nil {
		return TransactionIntent{}, false, err
	}
	tuple, err := subslice(encoded, tupleOffset, len(encoded)-tupleOffset)
	if err != nil {
		return TransactionIntent{}, false, err
	}
	if err := requireWords(tuple, 10); err != nil {
		return TransactionIntent{}, false, err
	}
	if err := requireCleanAddresses(tuple, 0, 1, 4); err != nil {
		return TransactionIntent{}, false, err
	}
	if err := requireUintBits(tuple, 6, 128); err != nil {
		return TransactionIntent{}, false, err
	}
	if err := requireUintBits(tuple, 7, 128); err != nil {
		return TransactionIntent{}, false, err
	}
	fee, err := readUint24(tuple, 2)
	if err != nil {
		return TransactionIntent{}, false, err
	}
	tickSpacing, err := readInt24(tuple, 3)
	if err != nil {
		return TransactionIntent{}, false, err
	}
	zeroForOne, err := readBool(tuple, 5)
	if err != nil {
		return TransactionIntent{}, false, err
	}
	hookOffset, err := readOffset(tuple, 9)
	if err != nil {
		return TransactionIntent{}, false, err
	}
	if _, err := readDynamicBytes(tuple, hookOffset); err != nil {
		return TransactionIntent{}, false, err
	}
	key := PoolKey{Currency0: readAddress(tuple, 0), Currency1: readAddress(tuple, 1), Fee: fee, TickSpacing: tickSpacing, Hooks: readAddress(tuple, 4)}
	id, err := PoolID(key)
	if err != nil {
		return TransactionIntent{}, false, malformedCalldata(err.Error())
	}
	registration, ok := p.registry.LookupPool(id)
	if !ok {
		return TransactionIntent{}, false, nil
	}
	currencyIn, currencyOut := key.Currency1, key.Currency0
	if zeroForOne {
		currencyIn, currencyOut = key.Currency0, key.Currency1
	}
	kind := IntentUnknown
	switch {
	case currencyOut == registration.Token && currencyIn == registration.Quote:
		kind = IntentBuy
	case currencyIn == registration.Token && currencyOut == registration.Quote:
		kind = IntentSell
	default:
		return TransactionIntent{}, false, nil
	}
	intent := baseIntent(call, registration.Protocol, kind)
	intent.Recipient = call.sender
	intent.Token = registration.Token
	intent.Quote = registration.Quote
	intent.CurrencyIn = currencyIn
	intent.CurrencyOut = currencyOut
	intent.PoolID = id
	intent.PoolKey = key
	intent.AmountIn = readBig(tuple, 6)
	intent.MinimumAmountOut = readBig(tuple, 7)
	intent.Deadline = new(big.Int).Set(deadline)
	return intent, true, nil
}

func parsePonsLaunchAndBuy(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 7); err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(args, 2, 5); err != nil {
		return TransactionIntent{}, err
	}
	intent := baseIntent(call, ProtocolPonsV2, IntentLaunchAndBuy)
	launch, err := decodePonsLaunchIntent(args, 0, 1, 2, 6, call.sender)
	if err != nil {
		return TransactionIntent{}, err
	}
	switch call.ponsDeployer {
	case ponsDeployerCreator:
		launch.OriginalDeployer = launch.Params.CreatorFeeRecipient
	case ponsDeployerUnknown:
		launch.OriginalDeployer = common.Address{}
	}
	intent.PonsLaunch = launch
	intent.Quote = readAddress(args, 2)
	intent.CurrencyIn = intent.Quote
	intent.AmountIn = readBig(args, 3)
	intent.MinimumAmountOut = readBig(args, 4)
	intent.Recipient = readAddress(args, 5)
	return intent, nil
}

func parsePonsLaunch(call intentCall, args []byte, hasExemptions bool) (TransactionIntent, error) {
	headWords := 3
	if hasExemptions {
		headWords = 4
	}
	if err := requireWords(args, headWords); err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(args, 2); err != nil {
		return TransactionIntent{}, err
	}
	exemptionsWord := -1
	if hasExemptions {
		exemptionsWord = 3
	}
	launch, err := decodePonsLaunchIntent(args, 0, 1, 2, exemptionsWord, call.sender)
	if err != nil {
		return TransactionIntent{}, err
	}
	switch call.ponsDeployer {
	case ponsDeployerCreator:
		launch.OriginalDeployer = launch.Params.CreatorFeeRecipient
	case ponsDeployerUnknown:
		launch.OriginalDeployer = common.Address{}
	}
	intent := baseIntent(call, ProtocolPonsV2, IntentLaunch)
	intent.Recipient = call.sender
	intent.Quote = readAddress(args, 2)
	intent.PonsLaunch = launch
	return intent, nil
}

func decodePonsLaunchIntent(args []byte, paramsWord, configWord, quoteWord, exemptionsWord int, originalDeployer common.Address) (*PonsLaunchIntent, error) {
	paramsOffset, err := readOffset(args, paramsWord)
	minHeadWords := quoteWord + 1
	if exemptionsWord >= 0 && exemptionsWord+1 > minHeadWords {
		minHeadWords = exemptionsWord + 1
	}
	if err != nil || paramsOffset < minHeadWords*abiWordSize {
		return nil, malformedCalldata("invalid Pons launch params offset")
	}
	params, err := subslice(args, paramsOffset, len(args)-paramsOffset)
	if err != nil || requireWords(params, 10) != nil {
		return nil, malformedCalldata("invalid Pons token params")
	}
	if err := requireCleanAddresses(params, 5); err != nil {
		return nil, err
	}
	if err := requireUintBits(params, 6, 16); err != nil {
		return nil, err
	}
	buyback, err := readBool(params, 7)
	if err != nil {
		return nil, err
	}
	name, err := readBoundedString(params, 0, 10*abiWordSize, maxPonsNameBytes)
	if err != nil {
		return nil, err
	}
	symbol, err := readBoundedString(params, 1, 10*abiWordSize, maxPonsSymbolBytes)
	if err != nil {
		return nil, err
	}
	logo, err := readBoundedString(params, 2, 10*abiWordSize, maxPonsLogoBytes)
	if err != nil {
		return nil, err
	}
	description, err := readBoundedString(params, 3, 10*abiWordSize, maxPonsDescriptionBytes)
	if err != nil {
		return nil, err
	}
	socialOffset, err := readOffset(params, 4)
	if err != nil || socialOffset < 10*abiWordSize {
		return nil, malformedCalldata("invalid Pons socials offset")
	}
	socialData, err := subslice(params, socialOffset, len(params)-socialOffset)
	if err != nil || requireWords(socialData, 5) != nil {
		return nil, malformedCalldata("invalid Pons socials")
	}
	socials := PonsSocials{}
	fields := []*string{&socials.Twitter, &socials.Telegram, &socials.Discord, &socials.Website, &socials.Farcaster}
	for index, destination := range fields {
		value, err := readBoundedString(socialData, index, 5*abiWordSize, maxPonsSocialBytes)
		if err != nil {
			return nil, err
		}
		*destination = value
	}
	result := &PonsLaunchIntent{
		Params: PonsTokenParams{
			Name: name, Symbol: symbol, Logo: logo, Description: description, Socials: socials,
			CreatorFeeRecipient: readAddress(params, 5), CreatorTaxBPS: uint16(readBig(params, 6).Uint64()),
			BuybackEnabled: buyback, ExpectedEconomics: common.BytesToHash(params[8*abiWordSize : 9*abiWordSize]),
			Salt: common.BytesToHash(params[9*abiWordSize : 10*abiWordSize]),
		},
		LaunchConfigID: readBig(args, configWord), OriginalDeployer: originalDeployer,
	}
	if exemptionsWord >= 0 {
		offset, err := readOffset(args, exemptionsWord)
		if err != nil || offset < minHeadWords*abiWordSize {
			return nil, malformedCalldata("invalid Pons exemptions offset")
		}
		result.SnipeTaxExemptions, err = readAddressArray(args, offset)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func readBoundedString(data []byte, word, minimumOffset, maximumBytes int) (string, error) {
	offset, err := readOffset(data, word)
	if err != nil || offset < minimumOffset {
		return "", malformedCalldata("invalid Pons string offset")
	}
	value, err := readDynamicBytes(data, offset)
	if err != nil {
		return "", err
	}
	if len(value) > maximumBytes {
		return "", malformedCalldata("Pons metadata exceeds contract limit")
	}
	return string(value), nil
}

func readAddressArray(data []byte, offset int) ([]common.Address, error) {
	lengthWord, err := subslice(data, offset, abiWordSize)
	if err != nil {
		return nil, err
	}
	length, err := wordToInt(lengthWord)
	if err != nil || length > maxNestedCalls {
		return nil, malformedCalldata("invalid Pons exemptions length")
	}
	byteLength, err := checkedProduct(length, abiWordSize)
	if err != nil {
		return nil, err
	}
	values, err := subslice(data, offset+abiWordSize, byteLength)
	if err != nil {
		return nil, err
	}
	result := make([]common.Address, length)
	for index := range length {
		if err := requireCleanAddresses(values, index); err != nil {
			return nil, err
		}
		result[index] = readAddress(values, index)
	}
	return result, nil
}

func parseO1LaunchAndBuy(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 2); err != nil {
		return TransactionIntent{}, err
	}
	launchOffset, err := readOffset(args, 0)
	if err != nil {
		return TransactionIntent{}, err
	}
	buyOffset, err := readOffset(args, 1)
	if err != nil {
		return TransactionIntent{}, err
	}
	launch, err := subslice(args, launchOffset, 5*abiWordSize)
	if err != nil {
		return TransactionIntent{}, err
	}
	buy, err := subslice(args, buyOffset, 4*abiWordSize)
	if err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(launch, 4); err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(buy, 0); err != nil {
		return TransactionIntent{}, err
	}
	intent := baseIntent(call, ProtocolO1, IntentLaunchAndBuy)
	intent.Recipient = call.sender
	intent.Quote = readAddress(launch, 4)
	intent.CurrencyIn = readAddress(buy, 0)
	intent.AmountIn = readBig(buy, 1)
	intent.MinimumAmountOut = readBig(buy, 2)
	return intent, nil
}

func parseO1Launch(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 1); err != nil {
		return TransactionIntent{}, err
	}
	launchOffset, err := readOffset(args, 0)
	if err != nil || launchOffset != abiWordSize {
		return TransactionIntent{}, malformedCalldata("invalid o1 launch tuple offset")
	}
	launch, err := subslice(args, launchOffset, 8*abiWordSize)
	if err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(launch, 4); err != nil {
		return TransactionIntent{}, err
	}
	intent := baseIntent(call, ProtocolO1, IntentLaunch)
	intent.Recipient = call.sender
	intent.Quote = readAddress(launch, 4)
	return intent, nil
}

func parseO1FeePolicy(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 1); err != nil {
		return TransactionIntent{}, err
	}
	offset, err := readOffset(args, 0)
	if err != nil || offset != abiWordSize {
		return TransactionIntent{}, malformedCalldata("invalid o1 fee configuration tuple offset")
	}
	configuration, err := subslice(args, offset, 4*abiWordSize)
	if err != nil {
		return TransactionIntent{}, err
	}
	base, err := readUint32(configuration, 0)
	if err != nil || base > 1_000 {
		return TransactionIntent{}, malformedCalldata("invalid o1 base fee")
	}
	start, err := readUint32(configuration, 1)
	if err != nil || start < base || start > 9_900 {
		return TransactionIntent{}, malformedCalldata("invalid o1 anti-snipe start fee")
	}
	window, err := readUint32(configuration, 2)
	if err != nil || window == 0 {
		return TransactionIntent{}, malformedCalldata("invalid o1 anti-snipe window")
	}
	intent := baseIntent(call, ProtocolO1, IntentFeePolicy)
	intent.FeePolicy = &FeePolicy{BaseFeeBPS: base, AntiSnipeStartTotalBPS: start, AntiSnipeWindowSeconds: window}
	return intent, nil
}

func parsePAIRLaunch(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 1); err != nil {
		return TransactionIntent{}, err
	}
	tupleOffset, err := readOffset(args, 0)
	if err != nil {
		return TransactionIntent{}, err
	}
	tuple, err := subslice(args, tupleOffset, len(args)-tupleOffset)
	if err != nil {
		return TransactionIntent{}, err
	}
	if err := requireWords(tuple, 11); err != nil {
		return TransactionIntent{}, err
	}
	intent := baseIntent(call, ProtocolPAIR, IntentLaunch)
	index, err := readUint8(tuple, 7)
	if err != nil {
		return TransactionIntent{}, err
	}
	if index == noDeveloperBuy {
		return intent, nil
	}
	allocationsOffset, err := readOffset(tuple, 4)
	if err != nil {
		return TransactionIntent{}, err
	}
	allocations, err := newStaticTupleArray(tuple, allocationsOffset, 2)
	if err != nil {
		return TransactionIntent{}, err
	}
	if int(index) >= allocations.length {
		return TransactionIntent{}, malformedCalldata("PAIR developer buy index out of bounds")
	}
	allocation, err := allocations.at(int(index))
	if err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(tuple, 6); err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(allocation, 0); err != nil {
		return TransactionIntent{}, err
	}
	intent.Kind = IntentLaunchAndBuy
	intent.Recipient = readAddress(tuple, 6)
	intent.Quote = readAddress(allocation, 0)
	intent.CurrencyIn = intent.Quote
	intent.RequestedAmountOut = readBig(tuple, 8)
	intent.MaximumAmountIn = readBig(tuple, 9)
	return intent, nil
}

func parseLongCreate(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 1); err != nil {
		return TransactionIntent{}, err
	}
	tupleOffset, err := readOffset(args, 0)
	if err != nil {
		return TransactionIntent{}, err
	}
	if tupleOffset != abiWordSize {
		return TransactionIntent{}, malformedCalldata("invalid Long create tuple offset")
	}
	tuple, err := subslice(args, tupleOffset, len(args)-tupleOffset)
	if err != nil {
		return TransactionIntent{}, err
	}
	const headWords = 13
	if err := requireWords(tuple, headWords); err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(tuple, 2, 3, 5, 7, 9, 11); err != nil {
		return TransactionIntent{}, err
	}
	readData := func(word int) ([]byte, error) {
		offset, offsetErr := readOffset(tuple, word)
		if offsetErr != nil || offset < headWords*abiWordSize {
			return nil, malformedCalldata("invalid Long create data offset")
		}
		return readDynamicBytes(tuple, offset)
	}
	tokenFactoryData, err := readData(4)
	if err != nil {
		return TransactionIntent{}, err
	}
	governanceFactoryData, err := readData(6)
	if err != nil {
		return TransactionIntent{}, err
	}
	poolInitializerData, err := readData(8)
	if err != nil {
		return TransactionIntent{}, err
	}
	liquidityMigratorData, err := readData(10)
	if err != nil {
		return TransactionIntent{}, err
	}

	long := &LongLaunchIntent{
		InitialSupply:         readBig(tuple, 0),
		NumTokensToSell:       readBig(tuple, 1),
		Numeraire:             readAddress(tuple, 2),
		TokenFactory:          readAddress(tuple, 3),
		TokenFactoryData:      tokenFactoryData,
		GovernanceFactory:     readAddress(tuple, 5),
		GovernanceFactoryData: governanceFactoryData,
		PoolInitializer:       readAddress(tuple, 7),
		PoolInitializerData:   poolInitializerData,
		LiquidityMigrator:     readAddress(tuple, 9),
		LiquidityMigratorData: liquidityMigratorData,
		Integrator:            readAddress(tuple, 11),
		Salt:                  common.BytesToHash(tuple[12*abiWordSize : 13*abiWordSize]),
	}
	intent := baseIntent(call, ProtocolLong, IntentLaunch)
	intent.Quote = long.Numeraire
	intent.LongLaunch = long
	return intent, nil
}

func executeModeCalls(args []byte) (dynamicCallArray, error) {
	if err := requireWords(args, 2); err != nil {
		return dynamicCallArray{}, err
	}
	offset, err := readOffset(args, 1)
	if err != nil || offset < 2*abiWordSize {
		return dynamicCallArray{}, malformedCalldata("invalid execute payload offset")
	}
	payload, err := readDynamicBytes(args, offset)
	if err != nil {
		return dynamicCallArray{}, err
	}
	return newDynamicCallArray(payload, 3)
}

func (p *Parser) parseAtomicLaunchV2(call intentCall, args []byte) (TransactionIntent, error) {
	if err := requireWords(args, 1); err != nil {
		return TransactionIntent{}, err
	}
	tupleOffset, err := readOffset(args, 0)
	if err != nil || tupleOffset < abiWordSize {
		return TransactionIntent{}, malformedCalldata("invalid atomic launch tuple offset")
	}
	tuple, err := subslice(args, tupleOffset, len(args)-tupleOffset)
	if err != nil {
		return TransactionIntent{}, err
	}
	if err := requireWords(tuple, 12); err != nil {
		return TransactionIntent{}, err
	}
	if err := requireCleanAddresses(tuple, 2, 3, 4); err != nil {
		return TransactionIntent{}, err
	}
	paramsOffset, err := readOffset(tuple, 0)
	if err != nil || paramsOffset < 12*abiWordSize {
		return TransactionIntent{}, malformedCalldata("invalid atomic launch params offset")
	}
	if _, err := subslice(tuple, paramsOffset, 10*abiWordSize); err != nil {
		return TransactionIntent{}, err
	}
	if _, err := readBool(tuple, 11); err != nil {
		return TransactionIntent{}, err
	}
	semanticCall := call
	semanticCall.contract = p.addresses.PonsFactory
	intent := baseIntent(semanticCall, ProtocolPonsV2, IntentLaunch)
	intent.Recipient = call.sender
	intent.Quote = readAddress(tuple, 2)
	launch, err := decodePonsLaunchIntent(tuple, 0, 1, 2, -1, common.Address{})
	if err != nil {
		return TransactionIntent{}, err
	}
	intent.PonsLaunch = launch
	return intent, nil
}

func (p *Parser) parseEmbeddedPonsLaunch(call intentCall) (TransactionIntent, error) {
	var result TransactionIntent
	found := false
	for offset := 4; offset+4 <= len(call.data); offset += abiWordSize {
		if [4]byte(call.data[offset:offset+4]) != selectorPonsLaunchAndBuy {
			continue
		}
		child := call
		child.contract = p.addresses.PonsLaunchAndBuy
		child.data = call.data[offset:]
		child.value = new(big.Int)
		child.ponsDeployer = ponsDeployerUnknown
		intent, err := parsePonsLaunchAndBuy(child, child.data[4:])
		if err != nil {
			continue
		}
		if found {
			return TransactionIntent{}, malformedCalldata("multiple embedded Pons launch payloads")
		}
		intent.Kind = IntentLaunch
		intent.CurrencyIn = common.Address{}
		intent.AmountIn = nil
		intent.MinimumAmountOut = nil
		result, found = intent, true
	}
	if !found {
		return TransactionIntent{}, malformedCalldata("embedded Pons launch payload is missing")
	}
	return result, nil
}

type dynamicCallArray struct {
	data      []byte
	tableBase int
	length    int
	headWords int
	valueWord int
	dataWord  int
	authWord  int
	boolWord  int
}

func newDynamicCallArray(data []byte, headWords int) (dynamicCallArray, error) {
	if headWords != 3 && headWords != 4 {
		return dynamicCallArray{}, malformedCalldata("unsupported call tuple")
	}
	authWord := -1
	if headWords == 4 {
		authWord = 3
	}
	return newDynamicCallArrayLayout(data, headWords, 1, 2, authWord, -1)
}

func newMulticall3ValueArray(data []byte) (dynamicCallArray, error) {
	return newDynamicCallArrayLayout(data, 4, 2, 3, -1, 1)
}

func newDynamicCallArrayLayout(data []byte, headWords, valueWord, dataWord, authWord, boolWord int) (dynamicCallArray, error) {
	offset, err := readOffset(data, 0)
	if err != nil || offset < abiWordSize {
		return dynamicCallArray{}, malformedCalldata("invalid call array offset")
	}
	lengthWord, err := subslice(data, offset, abiWordSize)
	if err != nil {
		return dynamicCallArray{}, err
	}
	length, err := wordToInt(lengthWord)
	if err != nil {
		return dynamicCallArray{}, err
	}
	if length > maxNestedCalls {
		return dynamicCallArray{}, malformedCalldata("too many nested calls")
	}
	tableBase, err := checkedSum(offset, abiWordSize)
	if err != nil {
		return dynamicCallArray{}, err
	}
	tableLength, err := checkedProduct(length, abiWordSize)
	if err != nil {
		return dynamicCallArray{}, err
	}
	if _, err := subslice(data, tableBase, tableLength); err != nil {
		return dynamicCallArray{}, err
	}
	return dynamicCallArray{
		data: data, tableBase: tableBase, length: length, headWords: headWords,
		valueWord: valueWord, dataWord: dataWord, authWord: authWord, boolWord: boolWord,
	}, nil
}

func (a dynamicCallArray) at(parent intentCall, index int) (intentCall, error) {
	contract, data, tuple, err := a.decode(index)
	if err != nil {
		return intentCall{}, err
	}
	return intentCall{
		transaction:  parent.transaction,
		sender:       parent.sender,
		contract:     contract,
		data:         data,
		value:        readBig(tuple, a.valueWord),
		ponsDeployer: parent.ponsDeployer,
	}, nil
}

func (a dynamicCallArray) view(index int) (common.Address, []byte, error) {
	contract, data, _, err := a.decode(index)
	return contract, data, err
}

func (a dynamicCallArray) decode(index int) (common.Address, []byte, []byte, error) {
	if index < 0 || index >= a.length {
		return common.Address{}, nil, nil, malformedCalldata("call array index out of bounds")
	}
	relative, err := readOffset(a.data[a.tableBase:], index)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	tupleBase, err := checkedSum(a.tableBase, relative)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	tuple, err := subslice(a.data, tupleBase, len(a.data)-tupleBase)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if err := requireWords(tuple, a.headWords); err != nil {
		return common.Address{}, nil, nil, err
	}
	if err := requireCleanAddresses(tuple, 0); err != nil {
		return common.Address{}, nil, nil, err
	}
	if a.boolWord >= 0 {
		if _, err := readBool(tuple, a.boolWord); err != nil {
			return common.Address{}, nil, nil, err
		}
	}
	dataOffset, err := readOffset(tuple, a.dataWord)
	if err != nil || dataOffset < a.headWords*abiWordSize {
		return common.Address{}, nil, nil, malformedCalldata("invalid nested calldata offset")
	}
	data, err := readDynamicBytes(tuple, dataOffset)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if a.authWord >= 0 {
		authOffset, err := readOffset(tuple, a.authWord)
		if err != nil || authOffset < a.headWords*abiWordSize {
			return common.Address{}, nil, nil, malformedCalldata("invalid nested authorization offset")
		}
		if _, err := readDynamicBytes(tuple, authOffset); err != nil {
			return common.Address{}, nil, nil, err
		}
	}
	return readAddress(tuple, 0), data, tuple, nil
}

func targetCallView(args []byte) (common.Address, []byte, error) {
	if err := requireWords(args, 3); err != nil {
		return common.Address{}, nil, err
	}
	if err := requireCleanAddresses(args, 0); err != nil {
		return common.Address{}, nil, err
	}
	dataOffset, err := readOffset(args, 1)
	if err != nil || dataOffset < 3*abiWordSize {
		return common.Address{}, nil, malformedCalldata("invalid target calldata offset")
	}
	data, err := readDynamicBytes(args, dataOffset)
	if err != nil {
		return common.Address{}, nil, err
	}
	return readAddress(args, 0), data, nil
}

func targetCall(parent intentCall, args []byte) (intentCall, error) {
	contract, data, err := targetCallView(args)
	if err != nil {
		return intentCall{}, err
	}
	return intentCall{
		transaction: parent.transaction, sender: parent.sender, contract: contract, data: data, value: readBig(args, 2),
		ponsDeployer: parent.ponsDeployer,
	}, nil
}

func pons7702CallData(args []byte) ([]byte, error) {
	if err := requireWords(args, 5); err != nil {
		return nil, err
	}
	dataOffset, err := readOffset(args, 3)
	if err != nil || dataOffset < 5*abiWordSize {
		return nil, malformedCalldata("invalid Pons 7702 calldata offset")
	}
	return readDynamicBytes(args, dataOffset)
}

func (p *Parser) pons7702Call(parent intentCall, args []byte) (intentCall, error) {
	data, err := pons7702CallData(args)
	if err != nil {
		return intentCall{}, err
	}
	return intentCall{
		transaction: parent.transaction, sender: parent.sender, contract: p.addresses.PonsLaunchAndBuy, data: data, value: readBig(args, 4),
		ponsDeployer: parent.ponsDeployer,
	}, nil
}

type dynamicBytesArray struct {
	data      []byte
	tableBase int
	length    int
}

func newDynamicBytesArray(data []byte, offset int) (dynamicBytesArray, error) {
	lengthWord, err := subslice(data, offset, abiWordSize)
	if err != nil {
		return dynamicBytesArray{}, err
	}
	length, err := wordToInt(lengthWord)
	if err != nil {
		return dynamicBytesArray{}, err
	}
	tableBase := offset + abiWordSize
	tableLength, err := checkedProduct(length, abiWordSize)
	if err != nil {
		return dynamicBytesArray{}, err
	}
	if _, err := subslice(data, tableBase, tableLength); err != nil {
		return dynamicBytesArray{}, err
	}
	return dynamicBytesArray{data: data, tableBase: tableBase, length: length}, nil
}

func (a dynamicBytesArray) at(index int) ([]byte, error) {
	if index < 0 || index >= a.length {
		return nil, malformedCalldata("dynamic array index out of bounds")
	}
	relative, err := readOffset(a.data[a.tableBase:], index)
	if err != nil {
		return nil, err
	}
	return readDynamicBytes(a.data, a.tableBase+relative)
}

type staticTupleArray struct {
	data      []byte
	start     int
	length    int
	wordCount int
}

func newStaticTupleArray(data []byte, offset, wordCount int) (staticTupleArray, error) {
	lengthWord, err := subslice(data, offset, abiWordSize)
	if err != nil {
		return staticTupleArray{}, err
	}
	length, err := wordToInt(lengthWord)
	if err != nil {
		return staticTupleArray{}, err
	}
	start := offset + abiWordSize
	arrayWords, err := checkedProduct(length, wordCount)
	if err != nil {
		return staticTupleArray{}, err
	}
	arrayLength, err := checkedProduct(arrayWords, abiWordSize)
	if err != nil {
		return staticTupleArray{}, err
	}
	if _, err := subslice(data, start, arrayLength); err != nil {
		return staticTupleArray{}, err
	}
	return staticTupleArray{data: data, start: start, length: length, wordCount: wordCount}, nil
}

func (a staticTupleArray) at(index int) ([]byte, error) {
	if index < 0 || index >= a.length {
		return nil, malformedCalldata("tuple array index out of bounds")
	}
	return subslice(a.data, a.start+index*a.wordCount*abiWordSize, a.wordCount*abiWordSize)
}

func methodSelector(signature string) [4]byte {
	hash := crypto.Keccak256([]byte(signature))
	return [4]byte(hash[:4])
}

func requireWords(data []byte, words int) error {
	_, err := subslice(data, 0, words*abiWordSize)
	return err
}

func subslice(data []byte, offset, length int) ([]byte, error) {
	if offset < 0 || length < 0 || offset > len(data) || length > len(data)-offset {
		return nil, malformedCalldata("ABI offset out of bounds")
	}
	return data[offset : offset+length], nil
}

func readBig(data []byte, word int) *big.Int {
	start := word * abiWordSize
	return new(big.Int).SetBytes(data[start : start+abiWordSize])
}

func readAddress(data []byte, word int) common.Address {
	start := word*abiWordSize + 12
	return common.BytesToAddress(data[start : start+common.AddressLength])
}

func readOffset(data []byte, word int) (int, error) {
	start := word * abiWordSize
	value, err := subslice(data, start, abiWordSize)
	if err != nil {
		return 0, err
	}
	offset, err := wordToInt(value)
	if err != nil || offset%abiWordSize != 0 {
		return 0, malformedCalldata("invalid ABI offset")
	}
	return offset, nil
}

func wordToInt(word []byte) (int, error) {
	if len(word) != abiWordSize || !allByte(word[:abiWordSize-8], 0) {
		return 0, malformedCalldata("ABI integer overflows platform int")
	}
	value := binary.BigEndian.Uint64(word[abiWordSize-8:])
	maxInt := uint64(^uint(0) >> 1)
	if value > maxInt {
		return 0, malformedCalldata("ABI integer overflows platform int")
	}
	return int(value), nil
}

func checkedProduct(left, right int) (int, error) {
	if left < 0 || right < 0 || (left != 0 && right > int(^uint(0)>>1)/left) {
		return 0, malformedCalldata("ABI length overflows platform int")
	}
	return left * right, nil
}

func checkedSum(left, right int) (int, error) {
	if left < 0 || right < 0 || right > int(^uint(0)>>1)-left {
		return 0, malformedCalldata("ABI offset overflows platform int")
	}
	return left + right, nil
}

func readDynamicBytes(data []byte, offset int) ([]byte, error) {
	lengthWord, err := subslice(data, offset, abiWordSize)
	if err != nil {
		return nil, err
	}
	length, err := wordToInt(lengthWord)
	if err != nil {
		return nil, err
	}
	return subslice(data, offset+abiWordSize, length)
}

func readUint24(data []byte, word int) (uint32, error) {
	start := word * abiWordSize
	value, err := subslice(data, start, abiWordSize)
	if err != nil || !allByte(value[:29], 0) {
		return 0, malformedCalldata("invalid uint24")
	}
	return uint32(value[29])<<16 | uint32(value[30])<<8 | uint32(value[31]), nil
}

func readUint32(data []byte, word int) (uint32, error) {
	value := readBig(data, word)
	if value == nil || !value.IsUint64() || value.Uint64() > uint64(^uint32(0)) {
		return 0, malformedCalldata("invalid uint32")
	}
	return uint32(value.Uint64()), nil
}

func readInt24(data []byte, word int) (int32, error) {
	start := word * abiWordSize
	value, err := subslice(data, start, abiWordSize)
	if err != nil {
		return 0, err
	}
	raw := uint32(value[29])<<16 | uint32(value[30])<<8 | uint32(value[31])
	if raw&0x800000 != 0 {
		if !allByte(value[:29], 0xff) {
			return 0, malformedCalldata("invalid int24 sign extension")
		}
		return int32(raw | 0xff000000), nil // #nosec G115 -- explicit int24 sign extension.
	}
	if !allByte(value[:29], 0) {
		return 0, malformedCalldata("invalid int24")
	}
	return int32(raw), nil // #nosec G115 -- raw is at most 0x7fffff here.
}

func readBool(data []byte, word int) (bool, error) {
	start := word * abiWordSize
	value, err := subslice(data, start, abiWordSize)
	if err != nil || !allByte(value[:31], 0) || value[31] > 1 {
		return false, malformedCalldata("invalid bool")
	}
	return value[31] == 1, nil
}

func readUint8(data []byte, word int) (byte, error) {
	start := word * abiWordSize
	value, err := subslice(data, start, abiWordSize)
	if err != nil || !allByte(value[:31], 0) {
		return 0, malformedCalldata("invalid uint8")
	}
	return value[31], nil
}

func requireCleanAddresses(data []byte, words ...int) error {
	for _, word := range words {
		value, err := subslice(data, word*abiWordSize, abiWordSize)
		if err != nil || !allByte(value[:12], 0) {
			return malformedCalldata("invalid address word")
		}
	}
	return nil
}

func requireUintBits(data []byte, word, bits int) error {
	value, err := subslice(data, word*abiWordSize, abiWordSize)
	if err != nil {
		return err
	}
	leading := abiWordSize - (bits+7)/8
	if bits <= 0 || bits > 256 || !allByte(value[:leading], 0) {
		return malformedCalldata("ABI unsigned integer exceeds declared width")
	}
	return nil
}

func allByte(data []byte, want byte) bool {
	for _, value := range data {
		if value != want {
			return false
		}
	}
	return true
}

func malformedCalldata(detail string) error {
	return fmt.Errorf("%w: %s", ErrMalformedCalldata, detail)
}
