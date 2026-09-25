package trade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

type workloadCounts struct{ accepted, deduped, queued, rejected int }

func (c workloadCounts) total() int { return c.accepted + c.deduped + c.queued + c.rejected }

func assertExecutionSafety(t *testing.T, store *Store, wallets, attempts int) {
	t.Helper()
	var gotAttempts, reservations, unresolved, nonceRows, nonceDistinct int
	queries := []struct {
		q   string
		dst *int
	}{
		{`SELECT COUNT(*) FROM transaction_attempts`, &gotAttempts},
		{`SELECT COUNT(*) FROM execution_reservations WHERE status IN ('signed','frozen')`, &reservations},
		{`SELECT COUNT(*) FROM execution_steps WHERE wallet_id IS NOT NULL AND status IN ('nonce_reserved','signing','signed','broadcast_unknown','reconciling','manual_resolution')`, &unresolved},
		{`SELECT COUNT(*) FROM transaction_attempts`, &nonceRows},
		{`SELECT COUNT(*) FROM (SELECT wallet_id,nonce FROM transaction_attempts GROUP BY wallet_id,nonce)`, &nonceDistinct},
	}
	for _, query := range queries {
		if err := store.db.QueryRow(query.q).Scan(query.dst); err != nil {
			t.Fatal(err)
		}
	}
	if gotAttempts != attempts || reservations != attempts || unresolved != wallets || nonceRows != nonceDistinct {
		t.Fatalf("attempts=%d reservations=%d unresolved=%d nonceRows=%d unique=%d", gotAttempts, reservations, unresolved, nonceRows, nonceDistinct)
	}
	rows, err := store.db.Query(`SELECT wallet_id,COUNT(*) FROM execution_steps WHERE wallet_id IS NOT NULL AND status IN ('nonce_reserved','signing','signed','broadcast_unknown','reconciling','manual_resolution') GROUP BY wallet_id HAVING COUNT(*)>1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("wallet has more than one unresolved execution step")
	}
	rows, err = store.db.Query(`SELECT r.input_amount,w.address FROM execution_reservations r JOIN dry_run_wallets w ON w.id=r.wallet_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var amount, address string
		if err = rows.Scan(&amount, &address); err != nil {
			t.Fatal(err)
		}
		value, ok := new(big.Int).SetString(amount, 10)
		if !ok || value.Sign() <= 0 || value.Cmp(new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil)) > 0 {
			t.Fatalf("reservation overcommit wallet=%s amount=%s", address, amount)
		}
	}
}

func TestC08HardeningSameIdentitySameWallet10000(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dry, _ := NewEngine(store, backend)
	req := buyRequest()
	if _, err := dry.DryRun(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	const total = 10000
	results := make(chan SignedArtifact, total)
	errs := make(chan error, total)
	var wg sync.WaitGroup
	for range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := kernel.Prepare(context.Background(), req.Intent.ID, req.WalletID)
			if err != nil {
				errs <- err
			} else {
				results <- a
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	counts := workloadCounts{}
	for err := range errs {
		t.Fatalf("unexpected rejection: %v", err)
	}
	var hash string
	for a := range results {
		if hash == "" {
			hash = a.TxHash
		}
		if a.TxHash != hash || a.Nonce != 7 {
			t.Fatal("artifact identity diverged")
		}
		if a.Duplicate {
			counts.deduped++
		} else {
			counts.accepted++
		}
	}
	if counts.total() != total || counts.accepted != 1 || counts.deduped != 9999 {
		t.Fatalf("counts=%+v", counts)
	}
	assertExecutionSafety(t, store, 1, 1)
}

func TestC08HardeningDifferentIdentitiesSameWallet10000(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	_ = store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address())
	dry, _ := NewEngine(store, backend)
	const total = 10000
	requests := make([]DryRunRequest, total)
	for i := range total {
		r := buyRequest()
		id := fmt.Sprintf("distinct-%05d", i)
		r.Intent.ID = id
		r.Intent.IdempotencyKey = id
		r.Intent.SourceEventID = id
		r.Intent.SourceObservationID = id
		requests[i] = r
		if result, err := dry.DryRun(context.Background(), r); err != nil || result.Status != "success" {
			t.Fatalf("seed %d err=%v", i, err)
		}
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	counts := runKernelWorkload(t, kernel, requests)
	if counts.total() != total || counts.accepted != 1 || counts.rejected != 9999 {
		t.Fatalf("counts=%+v", counts)
	}
	assertExecutionSafety(t, store, 1, 1)
}

func TestC08HardeningMultiWalletMixed10000(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	const wallets = 10
	const uniquePerWallet = 500
	requests := make([]DryRunRequest, 0, 10000)
	kernels := map[string]*ExecutionKernel{}
	backend := newFakeBackend()
	dry, _ := NewEngine(store, backend)
	for w := 0; w < wallets; w++ {
		key, _ := crypto.GenerateKey()
		signer, _ := NewLocalSigner(key)
		cipher, _ := NewAESGCMCipher(fmt.Sprintf("wallet-v%d", w), bytes.Repeat([]byte{byte(w + 1)}, 32))
		walletID := fmt.Sprintf("wallet-%02d", w)
		if err := store.RegisterDryRunWallet(context.Background(), walletID, signer.Address()); err != nil {
			t.Fatal(err)
		}
		kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
		kernels[walletID] = kernel
		for i := 0; i < uniquePerWallet; i++ {
			r := buyRequest()
			id := fmt.Sprintf("mixed-%02d-%04d", w, i)
			r.WalletID = walletID
			r.Intent.ID = id
			r.Intent.IdempotencyKey = id
			r.Intent.SourceEventID = id
			r.Intent.SourceObservationID = id
			if result, err := dry.DryRun(context.Background(), r); err != nil || result.Status != "success" {
				t.Fatalf("seed %s err=%v", id, err)
			}
			requests = append(requests, r, r)
		}
	}
	counts := runMultiKernelWorkload(t, kernels, requests)
	if counts.total() != len(requests) || counts.accepted != wallets || counts.deduped != wallets || counts.rejected != len(requests)-2*wallets {
		t.Fatalf("counts=%+v", counts)
	}
	assertExecutionSafety(t, store, wallets, wallets)
}

func runKernelWorkload(t *testing.T, kernel *ExecutionKernel, requests []DryRunRequest) workloadCounts {
	t.Helper()
	return runMultiKernelWorkload(t, map[string]*ExecutionKernel{"wallet-1": kernel}, requests)
}
func runMultiKernelWorkload(t *testing.T, kernels map[string]*ExecutionKernel, requests []DryRunRequest) workloadCounts {
	t.Helper()
	out := make(chan string, len(requests))
	var wg sync.WaitGroup
	for _, r := range requests {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := kernels[r.WalletID].Prepare(context.Background(), r.Intent.ID, r.WalletID)
			switch {
			case err == nil && !a.Duplicate:
				out <- "accepted"
			case err == nil && a.Duplicate:
				out <- "deduped"
			case errors.Is(err, ErrWalletLaneBusy):
				out <- "rejected"
			default:
				out <- "unexpected:" + fmt.Sprint(err)
			}
		}()
	}
	wg.Wait()
	close(out)
	var c workloadCounts
	for value := range out {
		switch value {
		case "accepted":
			c.accepted++
		case "deduped":
			c.deduped++
		case "rejected":
			c.rejected++
		default:
			t.Fatal(value)
		}
	}
	return c
}
