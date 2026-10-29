package tradeapi

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// Permanent version of the independent review's target-vs-seed attack. A
// helper that accidentally executes the setup order must NOT satisfy the HTTP
// composition acceptance just because fixture input values happen to match.
func TestLocalPostExecutionTargetParametersNotSeed(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			f := newLocalPostFixture(t, side)
			f.request.Fee.MaxGwei = "0.5"
			ttl := uint64(45)
			f.request.TTLSeconds = &ttl
			switch side {
			case "buy":
				f.request.Amount = "0.02"
			case "sell_amount":
				f.request.Amount = "500.25"
			case "sell_percent":
				f.request.SellPercent = "25"
			}
			value := "0"
			if side == "buy" {
				value = "20000000000000000"
			}
			f.signer.invoke = func(ctx context.Context, tx *types.Transaction, chain *big.Int) (*types.Transaction, error) {
				if tx.Value().String() != value || tx.GasFeeCap().String() != "500000000" || tx.GasTipCap().Sign() != 0 || tx.Nonce() != 23 || tx.ChainId().Uint64() != 4663 || chain.Uint64() != 4663 {
					t.Error("signer used seed/client-unbound transaction parameters")
				}
				return f.signer.signer.SignTransaction(ctx, tx, chain)
			}
			r := localPostRecord(t, call(f.h, "POST", "/v1/trades", "different-target", encode(f.request)), 202)
			wantInput := map[string]string{"buy": "20000000000000000", "sell_amount": "500250000", "sell_percent": "375125000"}[side]
			if r.Request.TTLSeconds == nil || *r.Request.TTLSeconds != ttl || r.ExpiresAt != testNow.Add(45*time.Second).Format(time.RFC3339Nano) || r.Validation.Output.Resolution.AmountIn != wantInput || r.Validation.Output.GasFeeCap != "500000000" || r.Execution.SubmissionState != "submitted" {
				t.Fatal("target POST was replaced by seed facts", encode(r))
			}
			f.assertOne(t, r, 1)
			var seedBindings, seedClaims int
			if err := f.store.db.QueryRow(`SELECT
 (SELECT COUNT(*) FROM manual_execution_bindings b JOIN manual_http_order_sources m ON m.operation_id=b.operation_id WHERE m.idempotency_key='b2-inputs'),
 (SELECT COUNT(*) FROM manual_nonce_claims q JOIN manual_http_order_sources m ON m.operation_id=q.operation_id WHERE m.idempotency_key='b2-inputs')`).Scan(&seedBindings, &seedClaims); err != nil || seedBindings != 0 || seedClaims != 0 {
				t.Fatal("setup source executed", seedBindings, seedClaims, err)
			}
			before := admittedDatabaseSnapshot(t, f.store.db)
			duplicate := localPostRecord(t, call(f.h, "POST", "/v1/trades", "different-target", encode(f.request)), 202)
			if !duplicate.Duplicate || duplicate.ExpiresAt != r.ExpiresAt || admittedDatabaseSnapshot(t, f.store.db) != before {
				t.Fatal("changed-target duplicate reran execution")
			}
			f.assertOne(t, r, 1)
		})
	}
}
