package manualnonce

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func pureFixture() (Facts, Observation, time.Time) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 123000000, time.UTC)
	f := Facts{OperationID: "op", RequestID: "request", BindingHash: strings.Repeat("a", 64), ChainID: 4663, WalletID: "wallet",
		WalletAddress: "0x" + strings.Repeat("1", 40), ContractAddress: "0x" + strings.Repeat("2", 40), RuntimeCodeHash: "0x" + strings.Repeat("3", 64),
		QuoteBlock: 10, QuoteHash: "0x" + strings.Repeat("4", 64), BoundAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Minute).Format(time.RFC3339Nano)}
	o := Observation{OperationID: f.OperationID, RequestID: f.RequestID, BindingHash: f.BindingHash, ChainID: f.ChainID, WalletID: f.WalletID,
		WalletAddress: f.WalletAddress, ContractAddress: f.ContractAddress, RuntimeCodeHash: f.RuntimeCodeHash, BlockNumber: f.QuoteBlock,
		BlockHash: f.QuoteHash, BlockCanonical: true, ConfirmedNonce: "0", PendingValue: "0", ObservedAt: f.BoundAt}
	return f, o, now
}

func TestManualNonceCanonicalUint64ZeroAndOverflow(t *testing.T) {
	for _, raw := range []string{"0", "1", "9223372036854775808", "18446744073709551614"} {
		if _, err := Nonce(raw); err != nil {
			t.Fatal("valid text nonce rejected", raw, err)
		}
		f, o, now := pureFixture()
		o.ConfirmedNonce = raw
		o.PendingValue = raw
		encoded := Encode(o)
		if _, err := Verify(encoded, Digest(encoded), raw, f, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"", "00", "01", "-1", "+1", " 1", "1.0", "1e2", "18446744073709551615", "18446744073709551616"} {
		if _, err := Nonce(raw); err == nil {
			t.Fatal("bad nonce accepted", raw)
		}
	}
}

func TestManualNonceObservationExactIdentityHashJSONAndTime(t *testing.T) {
	f, o, now := pureFixture()
	raw := Encode(o)
	for _, offset := range []time.Duration{0, MaxObservationAge - time.Nanosecond, MaxObservationAge} {
		if _, err := Verify(raw, Digest(raw), "0", f, now.Add(offset).Format(time.RFC3339Nano)); err != nil {
			t.Fatal("fresh boundary rejected", offset, err)
		}
	}
	for _, offset := range []time.Duration{-time.Nanosecond, MaxObservationAge + time.Nanosecond, time.Minute} {
		if _, err := Verify(raw, Digest(raw), "0", f, now.Add(offset).Format(time.RFC3339Nano)); err == nil {
			t.Fatal("time boundary accepted", offset)
		}
	}
	for _, bad := range []string{raw + " ", raw + raw, strings.Replace(raw, `"operation_id":"op"`, `"operation_id":"op","operation_id":"op"`, 1), strings.TrimSuffix(raw, "}") + `,"unknown":true}`, strings.Replace(raw, `"pending_nonce":"0"`, `"pending_nonce":0`, 1)} {
		if _, err := Verify(bad, Digest(bad), "0", f, now.Format(time.RFC3339Nano)); err == nil {
			t.Fatal("noncanonical/unknown JSON accepted", bad)
		}
	}
	if _, err := Verify(raw, strings.Repeat("0", 64), "0", f, f.BoundAt); err == nil {
		t.Fatal("digest substitution accepted")
	}
	bytes, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := DecodeFacts(string(bytes)); err != nil || decoded != f {
		t.Fatal("schema facts decode", err)
	}
	if _, err := DecodeFacts(string(bytes) + " "); err == nil {
		t.Fatal("noncanonical facts accepted")
	}
	for _, stamp := range []string{"", "invalid", strings.Replace(f.BoundAt, "Z", "+00:00", 1), "1969-12-31T23:59:59Z", "0001-01-01T00:00:00Z"} {
		if _, err := CanonicalTime(stamp); err == nil {
			t.Fatal("bad time accepted", stamp)
		}
	}
	f.ExpiresAt = now.Add(time.Nanosecond).Format(time.RFC3339Nano)
	if _, err := Verify(raw, Digest(raw), "0", f, f.ExpiresAt); err == nil {
		t.Fatal("expiry equality accepted")
	}
}

func TestManualNonceHistoricalFactsAndUnverifiableObservation(t *testing.T) {
	for _, kind := range []string{"operation", "request", "wallet", "chain", "address", "contract", "runtime", "quote", "canonical", "pending", "unknown_hash", "zero_address", "future", "pre_bound"} {
		t.Run(kind, func(t *testing.T) {
			f, o, now := pureFixture()
			switch kind {
			case "operation":
				o.OperationID = "other"
			case "request":
				o.RequestID = "other"
			case "wallet":
				o.WalletID = "other"
			case "chain":
				o.ChainID = 1
			case "address":
				o.WalletAddress = "0x" + strings.Repeat("9", 40)
			case "contract":
				o.ContractAddress = "0x" + strings.Repeat("9", 40)
			case "runtime":
				o.RuntimeCodeHash = "0x" + strings.Repeat("9", 64)
			case "quote":
				o.BlockNumber = 9
			case "canonical":
				o.BlockCanonical = false
			case "pending":
				o.PendingValue = "1"
			case "unknown_hash":
				o.BlockHash = "0x" + strings.Repeat("9", 64)
			case "zero_address":
				f.WalletAddress = "0x" + strings.Repeat("0", 40)
				o.WalletAddress = f.WalletAddress
			case "future":
				o.ObservedAt = now.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "pre_bound":
				o.ObservedAt = now.Add(-time.Nanosecond).Format(time.RFC3339Nano)
			}
			raw := Encode(o)
			if _, err := Verify(raw, Digest(raw), "0", f, now.Format(time.RFC3339Nano)); err == nil {
				t.Fatal("bad evidence accepted", kind)
			}
		})
	}
}
