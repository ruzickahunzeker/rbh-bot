package rbhparser

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

const (
	flapNonTaxVersion = uint8(2)
	flapTaxV3Version  = uint8(6)
	flapV2Migrator    = uint8(1)
	flapUniswapDEX    = uint8(0)
	flapTaxFreeState  = uint8(4)
)

type flapLaunchDraft struct {
	launch         Launch
	createdLog     gethtypes.Log
	taxLog         gethtypes.Log
	receiptIndexes []int
	createdSeen    bool
	curveSeen      bool
	thresholdSeen  bool
	versionSeen    bool
	quoteSeen      bool
	migratorSeen   bool
	dexSeen        bool
	taxSeen        bool
	asymmetricSeen bool
}

func (d flapLaunchDraft) complete() bool {
	return d.launch.Token != (common.Address{}) && d.curveSeen && d.thresholdSeen && d.versionSeen && d.quoteSeen && d.migratorSeen && d.dexSeen
}

func isFlapCreationTopic(topic common.Hash) bool {
	switch topic {
	case topicFlapLaunch, topicFlapCurve, topicFlapDexThreshold, topicFlapVersion,
		topicFlapQuote, topicFlapMigrator, topicFlapDexPreference, topicFlapTax,
		topicFlapAsymmetricTax:
		return true
	default:
		return false
	}
}

// collectFlapReceiptEvents follows the official indexing rule: TokenCreated and
// all configuration events must be interpreted together within one receipt.
func (p *Parser) collectFlapReceiptEvents(receipt *gethtypes.Receipt) ([]Event, map[int]struct{}, error) {
	drafts := make(map[common.Address]*flapLaunchDraft)
	consumed := make(map[int]struct{})
	for receiptIndex, logPtr := range receipt.Logs {
		if logPtr == nil || logPtr.Removed || logPtr.Address != p.addresses.FlapController || len(logPtr.Topics) == 0 || !isFlapCreationTopic(logPtr.Topics[0]) {
			continue
		}
		log := *logPtr
		if log.Topics[0] == topicFlapLaunch {
			launch, err := decodeFlapCreated(log)
			if err != nil {
				return nil, nil, fmt.Errorf("log %d: decode Flap TokenCreated: %w", log.Index, err)
			}
			draft := drafts[launch.Token]
			if draft == nil {
				draft = &flapLaunchDraft{}
				drafts[launch.Token] = draft
			}
			launch.CurveR, launch.CurveH, launch.CurveK = draft.launch.CurveR, draft.launch.CurveH, draft.launch.CurveK
			launch.DexSupplyThreshold = draft.launch.DexSupplyThreshold
			launch.TokenVersion, launch.MigratorType = draft.launch.TokenVersion, draft.launch.MigratorType
			launch.DexID, launch.LPFeeProfile = draft.launch.DexID, draft.launch.LPFeeProfile
			launch.Quote = draft.launch.Quote
			launch.BuyFeeBPS, launch.SellFeeBPS = draft.launch.BuyFeeBPS, draft.launch.SellFeeBPS
			draft.launch, draft.createdLog, draft.createdSeen = launch, log, true
			draft.receiptIndexes = append(draft.receiptIndexes, receiptIndex)
			continue
		}

		token, err := addressWord(log.Data, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("log %d: decode Flap token: %w", log.Index, err)
		}
		draft := drafts[token]
		if draft == nil {
			draft = &flapLaunchDraft{launch: Launch{Token: token}}
			drafts[token] = draft
		}
		draft.receiptIndexes = append(draft.receiptIndexes, receiptIndex)
		switch log.Topics[0] {
		case topicFlapCurve:
			if err := validateLog(log, 1, 128); err != nil {
				return nil, nil, fmt.Errorf("log %d: decode Flap curve: %w", log.Index, err)
			}
			draft.launch.CurveR, err = uintWord(log.Data, 1, 256)
			if err == nil {
				draft.launch.CurveH, err = uintWord(log.Data, 2, 256)
			}
			if err == nil {
				draft.launch.CurveK, err = uintWord(log.Data, 3, 256)
			}
			draft.curveSeen = err == nil
		case topicFlapDexThreshold:
			draft.launch.DexSupplyThreshold, err = decodeFlapTokenValue(log, token, 256)
			draft.thresholdSeen = err == nil
		case topicFlapVersion:
			var value uint64
			value, err = decodeFlapSmallValue(log, token, 8)
			draft.launch.TokenVersion, draft.versionSeen = uint8(value), err == nil
		case topicFlapQuote:
			if err = validateLog(log, 1, 64); err == nil {
				draft.launch.Quote, err = addressWord(log.Data, 1)
			}
			draft.quoteSeen = err == nil
		case topicFlapMigrator:
			var value uint64
			value, err = decodeFlapSmallValue(log, token, 8)
			draft.launch.MigratorType, draft.migratorSeen = uint8(value), err == nil
		case topicFlapDexPreference:
			if err = validateLog(log, 1, 96); err == nil {
				var dex, profile *big.Int
				dex, err = uintWord(log.Data, 1, 8)
				if err == nil {
					profile, err = uintWord(log.Data, 2, 8)
				}
				if err == nil {
					draft.launch.DexID, draft.launch.LPFeeProfile = uint8(dex.Uint64()), uint8(profile.Uint64())
				}
			}
			draft.dexSeen = err == nil
		case topicFlapTax, topicFlapAsymmetricTax:
			asymmetric := log.Topics[0] == topicFlapAsymmetricTax
			policy, policyErr := decodeFlapTaxPolicy(log, asymmetric)
			if policyErr != nil {
				err = policyErr
				break
			}
			if asymmetric || !draft.asymmetricSeen {
				draft.launch.BuyFeeBPS, draft.launch.SellFeeBPS = policy.BuyFeeBPS, policy.SellFeeBPS
				draft.taxLog = log
			}
			draft.taxSeen = true
			draft.asymmetricSeen = draft.asymmetricSeen || asymmetric
		}
		if err != nil {
			return nil, nil, fmt.Errorf("log %d: decode Flap configuration: %w", log.Index, err)
		}
	}

	ordered := make([]*flapLaunchDraft, 0, len(drafts))
	for _, draft := range drafts {
		if draft.createdSeen {
			for _, index := range draft.receiptIndexes {
				consumed[index] = struct{}{}
			}
		}
		if draft.complete() {
			ordered = append(ordered, draft)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].createdLog.Index < ordered[j].createdLog.Index })
	events := make([]Event, 0, len(ordered)*2)
	for _, draft := range ordered {
		launch := draft.launch
		if launch.Quote != (common.Address{}) || launch.MigratorType != flapV2Migrator || launch.DexID != flapUniswapDEX {
			continue
		}
		switch launch.TokenVersion {
		case flapNonTaxVersion:
			if draft.taxSeen && (launch.BuyFeeBPS != 0 || launch.SellFeeBPS != 0) {
				continue
			}
			launch.Protocol = ProtocolFlapStocks
			launch.BuyFeeBPS, launch.SellFeeBPS = 0, 0
		case flapTaxV3Version:
			if !draft.taxSeen {
				continue
			}
			launch.Protocol = ProtocolFlapTax
		default:
			continue
		}
		launch.Venue, launch.FeesVerified = p.addresses.FlapController, true
		event, ok, err := p.launchEvent(draft.createdLog, launch.Protocol, launch, nil)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			events = append(events, event)
			policyLog := draft.taxLog
			if policyLog.Topics == nil {
				policyLog = draft.createdLog
			}
			events = append(events, Event{Kind: EventFeePolicy, Protocol: launch.Protocol, Data: FeePolicy{
				Token: launch.Token, Rate: launch.BuyFeeBPS, Denominator: 10_000,
				BuyFeeBPS: launch.BuyFeeBPS, SellFeeBPS: launch.SellFeeBPS,
			}, Log: policyLog})
		}
	}
	return events, consumed, nil
}

func decodeFlapTokenValue(log gethtypes.Log, token common.Address, bits int) (*big.Int, error) {
	decodedToken, value, err := decodeFlapTokenAndUint(log, bits)
	if err != nil || decodedToken != token {
		return nil, ErrMalformedLog
	}
	return value, nil
}

func decodeFlapSmallValue(log gethtypes.Log, token common.Address, bits int) (uint64, error) {
	value, err := decodeFlapTokenValue(log, token, bits)
	if err != nil || !value.IsUint64() {
		return 0, ErrMalformedLog
	}
	return value.Uint64(), nil
}
