package feed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func verifyRPCReceipts(h *rpcHeader, receipts []rpcReceipt) ([]*types.Receipt, error) {
	if len(receipts) != len(h.Transactions) {
		return nil, fmt.Errorf("incomplete block receipt set")
	}
	seen := map[common.Hash]bool{}
	seenLogs := map[uint64]bool{}
	result := make([]*types.Receipt, 0, len(receipts))
	for i, v := range receipts {
		tx := common.HexToHash(v.TransactionHash)
		number, e1 := hexutil.DecodeUint64(v.BlockNumber)
		index, e2 := hexutil.DecodeUint64(v.TransactionIndex)
		status, e3 := hexutil.DecodeUint64(v.Status)
		if e1 != nil || e2 != nil || e3 != nil || status > 1 || len(v.TransactionHash) != 66 || tx == (common.Hash{}) || tx != h.Transactions[i] || seen[tx] || common.HexToHash(v.BlockHash) != h.Hash || number != uint64(h.Number) || index != uint64(i) || !common.IsHexAddress(v.From) {
			return nil, fmt.Errorf("receipt block/transaction identity mismatch")
		}
		seen[tx] = true
		out := &types.Receipt{TxHash: tx, BlockHash: h.Hash, BlockNumber: new(big.Int).SetUint64(number), TransactionIndex: uint(index), Status: status}
		for _, lg := range v.Logs {
			idx, e1 := hexutil.DecodeUint64(lg.LogIndex)
			bn, e2 := hexutil.DecodeUint64(lg.BlockNumber)
			ti, e3 := hexutil.DecodeUint64(lg.TransactionIndex)
			data, e4 := hexutil.Decode(lg.Data)
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || lg.Removed || seenLogs[idx] || bn != number || ti != index || common.HexToHash(lg.BlockHash) != h.Hash || common.HexToHash(lg.TransactionHash) != tx || !common.IsHexAddress(lg.Address) {
				return nil, fmt.Errorf("receipt log identity mismatch")
			}
			seenLogs[idx] = true
			log := &types.Log{Address: common.HexToAddress(lg.Address), Data: data, BlockNumber: bn, BlockHash: h.Hash, TxHash: tx, TxIndex: uint(index), Index: uint(idx)}
			for _, topic := range lg.Topics {
				if len(topic) != 66 {
					return nil, fmt.Errorf("invalid log topic")
				}
				b, e := hexutil.Decode(topic)
				if e != nil || len(b) != 32 {
					return nil, fmt.Errorf("invalid log topic")
				}
				log.Topics = append(log.Topics, common.BytesToHash(b))
			}
			out.Logs = append(out.Logs, log)
		}
		result = append(result, out)
	}
	return result, nil
}

func (r *RPCWSRunner) blockObservations(ctx context.Context, h *rpcHeader, raw []rpcReceipt, receipts []*types.Receipt) ([]Observation, error) {
	stamp := time.Unix(int64(h.Timestamp), 0).UTC()
	var result []Observation
	for i, receipt := range receipts {
		if receipt.Status != 1 {
			continue
		}
		handled := map[uint]bool{}
		if r.receipt.chainID == ChainID {
			events, err := r.parser.ParseReceipt(receipt)
			if err != nil {
				return nil, err
			}
			hydrated := false
			for _, lg := range receipt.Logs {
				if len(lg.Topics) > 0 && (lg.Topics[0] == parser.TopicPonsCurveBuy || lg.Topics[0] == parser.TopicPonsCurveSell) {
					if _, ok := r.parser.Registry().LookupCurve(lg.Address); !ok {
						err := r.bootstrapCurve(ctx, lg.Address, h)
						if errors.Is(err, errUnprovenCurve) {
							continue
						}
						if err != nil {
							return nil, err
						}
						hydrated = true
					}
				}
			}
			if hydrated {
				events, err = r.parser.ParseReceipt(receipt)
				if err != nil {
					return nil, err
				}
			}

			for _, event := range events {
				payload, ok := r.normalizeRBH(event, receipt, common.HexToAddress(raw[i].From))
				if !ok {
					continue
				}
				handled[event.Log.Index] = true
				obs, ok := r.normalizedObservation(h, receipt.TxHash, event.Log.Index, payload, stamp)
				if ok {
					result = append(result, obs)
				}
			}
		}
		// Keep standard V2/V3 and existing launch adapters. RPC-WS mode verifies
		// pool assets before associating any transfer with a swap.
		generic := r.receipt.extractBlockObservations(uint64(h.Number), []rpcReceipt{raw[i]}, stamp)
		for _, obs := range generic {
			var payload NormalizedSwapPayload
			if err := json.Unmarshal(obs.PayloadJSON, &payload); err != nil {
				return nil, err
			}
			if payload.PoolVersion == "v4" && r.receipt.chainID == ChainID {
				continue
			} // requires registry attribution above
			if payload.PoolVersion == "v2" || payload.PoolVersion == "v3" {
				token := common.HexToAddress(payload.Token)
				pool := common.HexToAddress(payload.PoolAddress)
				tokens := [2]common.Address{}
				for j, sig := range []string{"token0()", "token1()"} {
					v, err := r.contractAddress(ctx, pool, sig, h)
					if err != nil {
						return nil, err
					}
					tokens[j] = v
				}
				if token != tokens[0] && token != tokens[1] {
					continue
				}
				// Multiple trader assets make the legacy first-token heuristic ambiguous.
				// Only emit when exactly one non-quote pool asset has a trader delta.
				candidates := 0
				for _, t := range tokens {
					if t != r.receipt.wrappedNative && netTransfer(receipt, t, common.HexToAddress(payload.Trader)).Sign() != 0 {
						candidates++
					}
				}
				if candidates != 1 {
					continue
				}
			}
			// De-duplicate an SDK-normalized economic event at the same log.
			var logIndex uint64
			if _, err := fmt.Sscanf(obs.StableActionPath, "receipt/log/%d/", &logIndex); err != nil {
				return nil, err
			}
			if handled[uint(logIndex)] {
				continue
			}
			payload.EventTime = stamp
			payload.BlockHash = h.Hash.Hex()
			data, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			obs.PayloadJSON = data
			result = append(result, obs)
		}
	}
	return result, nil
}

func (r *RPCWSRunner) normalizedObservation(h *rpcHeader, tx common.Hash, index uint, p NormalizedSwapPayload, stamp time.Time) (Observation, bool) {
	if r.receipt.watchlist != nil && r.receipt.watchlist.Initialized() && !r.receipt.watchlist.Matches(p.Trader, p.Token) {
		return Observation{}, false
	}
	p.ChainID = r.receipt.chainID
	p.BlockNumber = uint64(h.Number)
	p.TxHash = strings.ToLower(tx.Hex())
	p.EventTime = stamp
	p.BlockHash = h.Hash.Hex()
	p.Stage = "included"
	p.TriggerID = ComputeTriggerID(p.ChainID, p.TxHash, p.Trader, p.Token, p.Side)
	data, err := json.Marshal(p)
	if err != nil {
		return Observation{}, false
	}
	return Observation{ChainID: p.ChainID, Source: SourceRPC, SourceSequence: p.BlockNumber, TransactionHash: tx, StableActionPath: fmt.Sprintf("receipt/log/%06d/%s/%s", index, p.PoolVersion, p.Side), PayloadJSON: data, ObservedAt: stamp}, true
}

func (r *RPCWSRunner) normalizeRBH(e parser.Event, receipt *types.Receipt, sender common.Address) (NormalizedSwapPayload, bool) {
	p := NormalizedSwapPayload{PoolAddress: strings.ToLower(e.Log.Address.Hex()), PoolVersion: e.Protocol.String(), Trader: strings.ToLower(sender.Hex())}
	var token common.Address
	var amount *big.Int
	switch v := e.Data.(type) {
	case parser.Launch:
		if e.Protocol != parser.ProtocolPonsV2 {
			return p, false
		}
		token = v.Token
		p.Side = "launch"
		p.PoolVersion = "pons-v2-curve"
		p.PoolAddress = strings.ToLower(v.Curve.Hex())
		amount = big.NewInt(0)
	case parser.CurveTrade:
		registration, ok := r.parser.Registry().LookupCurve(e.Log.Address)
		if !ok {
			return p, false
		}
		token = registration.Token
		p.Trader = strings.ToLower(v.BuyerOrSeller.Hex())
		p.PoolVersion = e.Protocol.String() + "-curve"
		if e.Kind == parser.EventCurveBuy {
			p.Side = "buy"
			amount = v.AmountOut
		} else if e.Kind == parser.EventCurveSell {
			p.Side = "sell"
			amount = v.AmountIn
		} else {
			return p, false
		}
	case parser.Swap:
		token = v.Registration.Token
		if token == (common.Address{}) || e.Protocol == parser.ProtocolUnknown {
			return p, false
		}
		delta := netTransfer(receipt, token, sender)
		if delta.Sign() == 0 {
			return p, false
		}
		p.Side = "buy"
		if delta.Sign() < 0 {
			p.Side = "sell"
		}
		amount = new(big.Int).Abs(delta)
		p.PoolVersion = e.Protocol.String() + "-v4"
	default:
		return p, false
	}
	if token == (common.Address{}) || amount == nil || (amount.Sign() <= 0 && p.Side != "launch") {
		return p, false
	}
	p.Token = strings.ToLower(token.Hex())
	p.TokenDeltaRaw = amount.String()
	return p, true
}

func netTransfer(receipt *types.Receipt, token, trader common.Address) *big.Int {
	net := new(big.Int)
	for _, lg := range receipt.Logs {
		if lg.Address != token || len(lg.Topics) != 3 || lg.Topics[0] != erc20TransferTopic || len(lg.Data) != 32 {
			continue
		}
		amount := new(big.Int).SetBytes(lg.Data)
		if common.BytesToAddress(lg.Topics[1].Bytes()) == trader {
			net.Sub(net, amount)
		}
		if common.BytesToAddress(lg.Topics[2].Bytes()) == trader {
			net.Add(net, amount)
		}
	}
	return net
}

func (r *RPCWSRunner) contractCall(ctx context.Context, address common.Address, data []byte, h *rpcHeader) ([]byte, error) {
	var raw hexutil.Bytes
	err := r.call(ctx, "eth_call", []any{map[string]any{"to": address.Hex(), "data": hexutil.Encode(data)}, hexutil.EncodeUint64(uint64(h.Number))}, &raw)
	return []byte(raw), err
}

func strictAddress(raw []byte) (common.Address, error) {
	if len(raw) != 32 || !bytes.Equal(raw[:12], make([]byte, 12)) {
		return common.Address{}, fmt.Errorf("invalid contract address word")
	}
	return common.BytesToAddress(raw), nil
}

func (r *RPCWSRunner) contractAddress(ctx context.Context, address common.Address, signature string, h *rpcHeader) (common.Address, error) {
	raw, err := r.contractCall(ctx, address, crypto.Keccak256([]byte(signature))[:4], h)
	if err != nil {
		return common.Address{}, err
	}
	return strictAddress(raw)
}

var errUnprovenCurve = errors.New("unproven Pons curve")

func (r *RPCWSRunner) bootstrapCurve(ctx context.Context, curve common.Address, h *rpcHeader) error {
	token, err := r.contractAddress(ctx, curve, "token()", h)
	if err != nil {
		return err
	}
	factory := r.parser.Addresses().PonsFactory
	raw, err := r.contractCall(ctx, factory, append(crypto.Keccak256([]byte("getLaunchedToken(address)"))[:4], common.LeftPadBytes(token.Bytes(), 32)...), h)
	if err != nil {
		return err
	}
	if len(raw) != 15*32 {
		return errUnprovenCurve
	}
	regToken, e1 := strictAddress(raw[:32])
	regCurve, e2 := strictAddress(raw[32:64])
	quote, e3 := strictAddress(raw[128:160])
	if e1 != nil || e2 != nil || e3 != nil || regToken != token || regCurve != curve || token == (common.Address{}) || new(big.Int).SetBytes(raw[448:480]).Cmp(big.NewInt(1)) != 0 {
		return errUnprovenCurve
	}
	return r.parser.Registry().RegisterCurve(parser.CurveRegistration{Curve: curve, Token: token, Quote: quote, Protocol: parser.ProtocolPonsV2})
}
