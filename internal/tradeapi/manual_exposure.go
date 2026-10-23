package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// Read-only accounting foundation, NOT an admission decision. The authoritative
// amounts remain in canary_risk_reservations; unit metadata is not a second
// balance ledger. A future writer must call this on its SAME BEGIN IMMEDIATE
// connection, then perform the complete atomic admission (not implemented here).
type manualExposure struct {
	WalletInput, GlobalInput                       map[manualrisk.Unit]manualrisk.Quantity
	WalletOutlay, GlobalOutlay                     manualrisk.Quantity
	WalletWindowOperations, GlobalWindowOperations uint64
	WalletActiveOperations                         uint64
}

type manualExposureRow struct {
	ID, Operation, Wallet, State, Created string
}

// Historical exposure must survive TTL/auth/stop changes. This verifier never
// calls the initial-admission contract reader: that reader correctly rejects
// expired contracts and busy wallets. Here we verify saved immutable source and
// quantities without granting historical rows renewed permission.
func verifyManualExposureRow(ctx context.Context, conn *sql.Conn, row manualExposureRow, now time.Time) (manualrisk.Quantity, manualrisk.Quantity, error) {
	r, fingerprint, err := scanManualRecord(conn.QueryRowContext(ctx, selectManualRecord+`WHERE m.operation_id=?`, row.Operation))
	if err != nil {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, manualVerificationError(err, "EXPOSURE_SOURCE_INVALID")
	}
	resolution, originalPolicy, sourceHash, found, err := readResolution(ctx, conn, row.Operation)
	if err != nil || !found {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, manualVerificationError(err, "EXPOSURE_RESOLUTION_INVALID")
	}
	current, currentHash, err := loadValidationPolicy(ctx, conn, originalPolicy.Version)
	if err != nil || current != originalPolicy || currentHash != sourceHash {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, manualVerificationError(err, "EXPOSURE_ORIGINAL_POLICY_INVALID")
	}
	var raw, digest, policyID, policyHash, asset, kind, expiry, stamp, parentInput, parentGas, parentToken, parentHash, quoteHash, savedFingerprint, savedResult string
	var chain, version, quote uint64
	var decimals uint8
	err = conn.QueryRowContext(ctx, `SELECT v.result_json,v.result_hash,u.manual_policy_id,u.manual_policy_hash,u.chain_id,u.input_asset,u.input_decimals,u.accounting_kind,u.expires_at,u.created_at,
 rr.input_amount,rr.gas_budget,rr.token_address,rr.source_hash,rr.policy_version,rr.quote_block_number,rr.quote_block_hash,u.request_fingerprint,u.result_hash
 FROM manual_reservation_units u JOIN canary_risk_reservations rr ON rr.id=u.reservation_id AND rr.operation_id=u.operation_id
 JOIN manual_trade_validations v ON v.operation_id=u.operation_id
 WHERE u.reservation_id=? AND u.operation_id=? AND rr.wallet_id=?`, row.ID, row.Operation, row.Wallet).Scan(
		&raw, &digest, &policyID, &policyHash, &chain, &asset, &decimals, &kind, &expiry, &stamp,
		&parentInput, &parentGas, &parentToken, &parentHash, &version, &quote, &quoteHash, &savedFingerprint, &savedResult)
	var out chainquery.ValidationOutput
	if err != nil || json.Unmarshal([]byte(raw), &out) != nil || encode(out) != raw || hash([]byte(raw)) != digest || out.Resolution != resolution ||
		savedResult != digest || parentHash != digest || savedFingerprint != fingerprint || expiry != r.ExpiresAt || stamp != row.Created || version != originalPolicy.Version ||
		parentInput != resolution.AmountIn || parentGas != out.GasBudget || parentToken != resolution.Token.Address || quote != resolution.Token.Block.Number || quoteHash != resolution.Token.Block.Hash {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, manualVerificationError(err, "EXPOSURE_BINDING_INVALID")
	}
	var wallet, apiRaw string
	if err = conn.QueryRowContext(ctx, `SELECT wallet_id,api_policy_json FROM manual_http_order_sources WHERE operation_id=?`, row.Operation).Scan(&wallet, &apiRaw); err != nil || wallet != row.Wallet {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, manualVerificationError(err, "EXPOSURE_WALLET_INVALID")
	}
	var api Policy
	if json.Unmarshal([]byte(apiRaw), &api) != nil {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, rejectManualInput("EXPOSURE_SOURCE_INVALID")
	}
	r.validationMaxFee = api.MaxFeeGwei
	intent, err := intentFromRecord(r, resolution.Wallet)
	if err != nil || chainquery.CheckValidation(intent, resolution, limitsFromPolicy(originalPolicy, r), out) != nil {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, rejectManualInput("EXPOSURE_RESULT_INVALID")
	}
	c := ManualExecutionContract{PolicyVersion: originalPolicy.Version, PolicyHash: sourceHash}
	p, gotHash, err := loadManualPolicy(ctx, conn, policyID, c, now)
	unit := manualrisk.Unit{ChainID: chain, Asset: asset, Decimals: decimals, Kind: kind}
	expected := manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}
	if resolution.Side == "sell" {
		expected.Asset, expected.Decimals = resolution.Token.Address, resolution.Token.Decimals
	}
	if err != nil || gotHash != policyHash || unit != expected {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, manualVerificationError(err, "EXPOSURE_UNIT_OR_POLICY_INVALID")
	}
	if _, err = p.Limit(unit); err != nil {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, rejectManualInput("EXPOSURE_UNIT_OR_POLICY_INVALID")
	}
	created, ce := canonicalManualTime(r.CreatedAt)
	expires, ee := canonicalManualTime(expiry)
	reserved, re := canonicalManualTime(stamp)
	if ce != nil || ee != nil || re != nil || reserved.Before(created) || !reserved.Before(expires) || reserved.After(now) {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, rejectManualInput("EXPOSURE_TIME_INVALID")
	}
	input := manualrisk.Quantity{Unit: unit, Units: parentInput}
	gas, err := manualrisk.GasBudget(strconv.FormatUint(out.GasLimit, 10), out.GasFeeCap, out.GasTipCap)
	if err != nil || gas.Units != parentGas || input.Validate() != nil || input.Units == "0" {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, rejectManualInput("EXPOSURE_QUANTITY_INVALID")
	}
	outlay, err := manualrisk.Outlay(manualrisk.Quantity{Unit: manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.NativeValue}, Units: out.Value}, gas)
	if err != nil {
		return manualrisk.Quantity{}, manualrisk.Quantity{}, rejectManualInput("EXPOSURE_QUANTITY_INVALID")
	}
	return input, outlay, nil
}

func addManualExposure(m map[manualrisk.Unit]manualrisk.Quantity, input manualrisk.Quantity) error {
	prior, found := m[input.Unit]
	if !found {
		prior = manualrisk.Quantity{Unit: input.Unit, Units: "0"}
	}
	updated, err := manualrisk.Add(prior, input)
	if err != nil {
		return rejectManualInput("EXPOSURE_OVERFLOW")
	}
	m[input.Unit] = updated
	return nil
}

// Counts use a conservative rolling inclusive window across ALL assets and
// policies. No usage projection is rebuilt, relabelled or reset. Any relevant
// legacy/untyped history rejects the calculation rather than guessing units.
func loadManualExposureTx(ctx context.Context, conn *sql.Conn, wallet string, p manualrisk.Policy, now time.Time) (manualExposure, error) {
	var zero manualExposure
	if ctx == nil || conn == nil || !validID(wallet) || p.Validate() != nil || now.IsZero() {
		return zero, ErrInvalid
	}
	cutoff := now.Add(-time.Duration(p.WindowSeconds) * time.Second)
	projection, err := conn.QueryContext(ctx, `SELECT w.window_start,p.window_seconds FROM canary_window_usage w LEFT JOIN canary_policies p ON p.version=w.policy_version`)
	if err != nil {
		return zero, manualVerificationError(err, "EXPOSURE_UNAVAILABLE")
	}
	for projection.Next() {
		var raw string
		var seconds uint64
		if err = projection.Scan(&raw, &seconds); err != nil {
			break
		}
		stamp, te := canonicalManualTime(raw)
		// A prior bucket can overlap this rolling window. Never decide
		// irrelevance solely from its start string (or reset the projection).
		if te != nil || seconds == 0 || seconds > 31536000 || !stamp.Add(time.Duration(seconds)*time.Second).Before(cutoff) {
			err = rejectManualInput("UNTYPED_WINDOW_HISTORY")
			break
		}
	}
	if err == nil {
		err = projection.Err()
	}
	projection.Close()
	if err != nil {
		return zero, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT id,operation_id,wallet_id,state,created_at FROM canary_risk_reservations ORDER BY id`)
	if err != nil {
		return zero, manualVerificationError(err, "EXPOSURE_UNAVAILABLE")
	}
	var relevant []manualExposureRow
	for rows.Next() {
		var row manualExposureRow
		if err = rows.Scan(&row.ID, &row.Operation, &row.Wallet, &row.State, &row.Created); err != nil {
			break
		}
		stamp, te := canonicalManualTime(row.Created)
		if te != nil || stamp.After(now) || !validID(row.Wallet) {
			err = rejectManualInput("EXPOSURE_TIME_OR_WALLET_INVALID")
			break
		}
		if row.State != "reserved" && row.State != "frozen" && row.State != "committed" && row.State != "released" {
			err = rejectManualInput("EXPOSURE_STATE_INVALID")
			break
		}
		if row.State == "reserved" || row.State == "frozen" || !stamp.Before(cutoff) {
			relevant = append(relevant, row)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return zero, err
	}
	u := manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.NativeOutlay}
	v := manualExposure{WalletInput: make(map[manualrisk.Unit]manualrisk.Quantity), GlobalInput: make(map[manualrisk.Unit]manualrisk.Quantity), WalletOutlay: manualrisk.Quantity{Unit: u, Units: "0"}, GlobalOutlay: manualrisk.Quantity{Unit: u, Units: "0"}}
	for _, row := range relevant {
		var typed int
		if err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_reservation_units WHERE reservation_id=? AND operation_id=?`, row.ID, row.Operation).Scan(&typed); err != nil || typed != 1 {
			return zero, manualVerificationError(err, "UNTYPED_RESERVATION_HISTORY")
		}
		input, outlay, err := verifyManualExposureRow(ctx, conn, row, now)
		if err != nil {
			return zero, err
		}
		stamp, _ := canonicalManualTime(row.Created)
		if !stamp.Before(cutoff) {
			v.GlobalWindowOperations++
			if row.Wallet == wallet {
				v.WalletWindowOperations++
			}
		}
		if row.State != "reserved" && row.State != "frozen" {
			continue
		}
		if _, err = p.Limit(input.Unit); err != nil {
			return zero, rejectManualInput("EXPOSURE_CURRENT_POLICY_UNIT_UNSUPPORTED")
		}
		if err = addManualExposure(v.GlobalInput, input); err != nil {
			return zero, err
		}
		v.GlobalOutlay, err = manualrisk.Add(v.GlobalOutlay, outlay)
		if err != nil {
			return zero, rejectManualInput("EXPOSURE_OVERFLOW")
		}
		if row.Wallet == wallet {
			v.WalletActiveOperations++
			if err = addManualExposure(v.WalletInput, input); err != nil {
				return zero, err
			}
			v.WalletOutlay, err = manualrisk.Add(v.WalletOutlay, outlay)
			if err != nil {
				return zero, rejectManualInput("EXPOSURE_OVERFLOW")
			}
		}
	}
	return v, nil
}
