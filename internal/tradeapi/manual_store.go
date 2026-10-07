package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// This source owns user intent, not chain observations, C07 attestations or
// execution policy. Those remain unbound until independently validated.
const selectManualRecord = `SELECT m.id,m.operation_id,m.request_json,m.request_fingerprint,m.api_policy_version,m.api_policy_hash,m.api_policy_json,m.expires_at,m.created_at,m.deadline_capability,m.principal_id,m.idempotency_key,m.wallet_id,m.wallet_address,m.step_id
 FROM manual_http_order_sources m `

func scanManualRecord(row interface{ Scan(...any) error }) (Record, string, error) {
	var r Record
	var operation, request, fingerprint, policyJSON, principal, key, walletID, walletAddress, step string
	err := row.Scan(&r.RequestID, &operation, &request, &fingerprint, &r.APIPolicyVersion, &r.APIPolicyHash, &policyJSON, &r.ExpiresAt, &r.CreatedAt, &r.DeadlineCapability, &principal, &key, &walletID, &walletAddress, &step)
	if errors.Is(err, sql.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if err != nil {
		return r, "", err
	}
	var p Policy
	if decodeRequest([]byte(request), &r.Request) != nil || json.Unmarshal([]byte(policyJSON), &p) != nil || p.validate() != nil || p.Version != r.APIPolicyVersion || encode(p) != policyJSON || hash([]byte(policyJSON)) != r.APIPolicyHash || hash([]byte(request)) != fingerprint {
		return Record{}, "", ErrInvalid
	}
	normalized, err := normalize(r.Request)
	if err != nil {
		return Record{}, "", err
	}
	created, createdErr := time.Parse(time.RFC3339Nano, r.CreatedAt)
	expires, expiresErr := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	ttl, policyErr := p.apply(normalized)
	if policyErr != nil || encode(normalized) != request || createdErr != nil || expiresErr != nil || created.UTC().Format(time.RFC3339Nano) != r.CreatedAt || expires.UTC().Format(time.RFC3339Nano) != r.ExpiresAt || !expires.Equal(created.Add(time.Duration(ttl)*time.Second)) || r.DeadlineCapability != "APPLICATION_TTL_ONLY" || operation != "manual_op_"+r.RequestID || step != "manual_step_"+r.RequestID || r.RequestID != "req_"+hash([]byte(principal+"\x00"+key)) || !(Identity{PrincipalID: principal, WalletID: walletID, WalletAddress: walletAddress}).valid() || !validID(key) || r.Request.WalletID != walletID {
		return Record{}, "", ErrInvalid
	}
	r.OperationID = &operation
	r.Status, r.Reason = "RECORDED", "VALIDATION_NOT_CONNECTED"
	return r, fingerprint, nil
}

// RecordManual serializes both historical rejection and new intake lookup.
// It persists only a pending validation operation and immutable source. There
// is no queue consumer, reservation, nonce, simulation, signing or send call.
func (s *Store) RecordManual(ctx context.Context, identity Identity, key string, request Request, policy Policy, now func() time.Time) (Record, error) {
	if s == nil || s.db == nil || ctx == nil || !identity.valid() || !validID(key) || now == nil || policy.validate() != nil {
		return Record{}, ErrInvalid
	}
	r, err := normalize(request)
	if err != nil {
		return Record{}, err
	}
	if r.WalletID != identity.WalletID {
		return Record{}, ErrWallet
	}
	encoded := encode(r)
	fingerprint := hash([]byte(encoded))
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Record{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Record{}, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	prior, priorFingerprint, err := scanRecord(conn.QueryRowContext(ctx, selectRecord+`WHERE principal_id=? AND idempotency_key=?`, identity.PrincipalID, key))
	if errors.Is(err, ErrNotFound) {
		prior, priorFingerprint, err = scanManualRecord(conn.QueryRowContext(ctx, selectManualRecord+`WHERE m.principal_id=? AND m.idempotency_key=?`, identity.PrincipalID, key))
	}
	if err == nil {
		if priorFingerprint != fingerprint {
			return Record{}, ErrConflict
		}
		prior.Duplicate = true
		_, err = conn.ExecContext(ctx, "COMMIT")
		return prior, err
	}
	if !errors.Is(err, ErrNotFound) {
		return Record{}, err
	}
	ttl, err := policy.apply(r)
	if err != nil {
		return Record{}, err
	}
	var address string
	err = conn.QueryRowContext(ctx, `SELECT address FROM dry_run_wallets WHERE id=? AND chain_id=4663 AND enabled=1`, identity.WalletID).Scan(&address)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrWallet
	}
	if err != nil {
		return Record{}, err
	}
	if !common.IsHexAddress(address) || common.HexToAddress(address) != common.HexToAddress(identity.WalletAddress) {
		return Record{}, ErrWallet
	}
	stamp := now().UTC()
	if stamp.IsZero() || stamp.Year() < 1970 || stamp.Year() > 9998 {
		return Record{}, ErrInvalid
	}
	policyJSON := encode(policy)
	id := "req_" + hash([]byte(identity.PrincipalID+"\x00"+key))
	operation, step := "manual_op_"+id, "manual_step_"+id
	record := Record{RequestID: id, OperationID: &operation, Status: "RECORDED", Reason: "VALIDATION_NOT_CONNECTED", Request: r, APIPolicyVersion: policy.Version, APIPolicyHash: hash([]byte(policyJSON)), CreatedAt: stamp.Format(time.RFC3339Nano), ExpiresAt: stamp.Add(time.Duration(ttl) * time.Second).Format(time.RFC3339Nano), DeadlineCapability: "APPLICATION_TTL_ONLY"}
	_, err = conn.ExecContext(ctx, `INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at,request_json,updated_at,deadline_capability,expires_at) VALUES(?,4663,?, ?,?,'swap','manual_validation_pending',?,?,?,?,?)`, operation, r.WalletID, "manual:"+id, fingerprint, record.CreatedAt, encoded, record.CreatedAt, record.DeadlineCapability, record.ExpiresAt)
	if err != nil {
		return Record{}, err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id,created_at,updated_at,expires_at) VALUES(?,?,0,'manual_trade_validation','validation_pending',?,?,?,?)`, step, operation, r.WalletID, record.CreatedAt, record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return Record{}, err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO manual_http_order_sources(id,principal_id,idempotency_key,operation_id,step_id,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,deadline_capability,contract_deadline,send_authorized,submission_queued) VALUES(?,?,?,?,?,'robinhood',?,?,'MANUAL',?,?,?,?,?,?,?,'APPLICATION_TTL_ONLY',0,0,0)`, id, identity.PrincipalID, key, operation, step, r.WalletID, address, fingerprint, encoded, policy.Version, record.APIPolicyHash, policyJSON, record.ExpiresAt, record.CreatedAt)
	if err != nil {
		return Record{}, err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return record, err
}
