package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type Store struct {
	db       *sql.DB
	database *storage.Database
}

func NewStore(database *storage.Database) (*Store, error) {
	if database == nil {
		return nil, ErrInvalid
	}
	db, err := database.SQLDB(storage.TradeOwner)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, database: database}, nil
}

// reader returns the query_only=1 read-only SQLite connection pool when available,
// falling back to the primary writer handle for in-memory test databases.
func (s *Store) reader() *sql.DB {
	if s != nil && s.database != nil {
		if rdb, err := s.database.ReadDB(storage.TradeOwner); err == nil && rdb != nil {
			return rdb
		}
	}
	if s != nil {
		return s.db
	}
	return nil
}

type Identity struct {
	PrincipalID   string
	WalletID      string
	WalletAddress string
}

func (i Identity) valid() bool {
	return validID(i.PrincipalID) && validID(i.WalletID) && common.IsHexAddress(i.WalletAddress) && common.HexToAddress(i.WalletAddress) != (common.Address{})
}

type Record struct {
	validationMaxFee   string
	Validation         *ManualValidation  `json:"validation,omitempty"`
	Admission          *ManualAdmission   `json:"admission,omitempty"`
	Execution          *ExecutionSnapshot `json:"execution,omitempty"`
	Progress           RecordProgress     `json:"progress"`
	RequestID          string             `json:"request_id"`
	OperationID        *string            `json:"operation_id"` // C1 identity only, never authorization.
	Status             string             `json:"status"`
	Reason             string             `json:"reason_code"`
	Request            Request            `json:"request"`
	APIPolicyVersion   uint64             `json:"api_policy_version"`
	APIPolicyHash      string             `json:"api_policy_hash"`
	ExpiresAt          string             `json:"expires_at"`
	CreatedAt          string             `json:"created_at"`
	DeadlineCapability string             `json:"deadline_capability"`
	ContractDeadline   bool               `json:"contract_deadline"`
	SendAuthorized     bool               `json:"send_authorized"`
	SubmissionQueued   bool               `json:"submission_queued"`
	Duplicate          bool               `json:"duplicate"`
	ContinuationActive bool               `json:"continuation_active,omitempty"`
}

// RecordProgress is a read-only projection of verified durable facts. It is
// neither a transaction outcome nor permission to sign, submit or send.
type RecordProgress struct {
	Stage string `json:"stage"`
}

func (r *Record) projectProgress() {
	switch {
	case r.Status == "REJECTED":
		r.Progress.Stage = "REJECTED"
	case r.Admission != nil:
		r.Progress.Stage = "ADMISSION_RECORDED"
	case r.Validation != nil:
		r.Progress.Stage = "VALIDATION_RECORDED"
	default:
		r.Progress.Stage = "INTAKE_RECORDED"
	}
}

func scanRecord(row interface{ Scan(...any) error }) (Record, string, error) {
	var r Record
	var request, fingerprint string
	err := row.Scan(&r.RequestID, &r.Status, &r.Reason, &request, &r.APIPolicyVersion, &r.APIPolicyHash, &r.ExpiresAt, &r.CreatedAt, &r.DeadlineCapability, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if err != nil {
		return r, "", err
	}
	err = decodeRequest([]byte(request), &r.Request)
	return r, fingerprint, err
}

const selectRecord = `SELECT id,outcome,reason_code,request_json,api_policy_version,api_policy_hash,expires_at,created_at,deadline_capability,request_fingerprint FROM user_http_trade_requests `
const selectMultiChainRecord = `SELECT id,outcome,reason_code,request_json,api_policy_version,api_policy_hash,expires_at,created_at,deadline_capability,request_fingerprint FROM multichain_http_trade_requests `

// RegisterMultiChainWallet registers an enabled wallet in multichain_wallets for non-4663 chains (e.g. BSC chain_id=56).
func (s *Store) RegisterMultiChainWallet(ctx context.Context, chainID uint64, walletID string, addr common.Address, now time.Time) error {
	if s == nil || s.db == nil || chainID == 0 || !validID(walletID) || addr == (common.Address{}) {
		return ErrInvalid
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO multichain_wallets(id, chain_id, address, enabled, created_at) VALUES(?,?,?,1,?)
		 ON CONFLICT(id) DO UPDATE SET address=excluded.address, enabled=1`,
		walletID, chainID, addr.Hex(), now.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// Reject serializes duplicate resolution and first creation across SQLite
// handles. Clock and defaults are consulted only for a genuinely new request.
// There is no path from this journal to execution workers, including restart.
func (s *Store) Reject(ctx context.Context, identity Identity, key string, request Request, policy Policy, now func() time.Time) (Record, error) {
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
	if errors.Is(err, ErrNotFound) {
		prior, priorFingerprint, err = scanRecord(conn.QueryRowContext(ctx, selectMultiChainRecord+`WHERE principal_id=? AND idempotency_key=?`, identity.PrincipalID, key))
	}
	if err == nil {
		if priorFingerprint != fingerprint {
			return Record{}, ErrConflict
		}
		prior.Duplicate = true
		_, err = conn.ExecContext(ctx, "COMMIT")
		prior.projectProgress()
		return prior, err
	}
	if !errors.Is(err, ErrNotFound) {
		return Record{}, err
	}
	ttl, err := policy.apply(r)
	if err != nil {
		return Record{}, err
	}
	if policy.expectedChain() != "robinhood" {
		var address string
		var chainID uint64
		if err = conn.QueryRowContext(ctx, `SELECT chain_id, address FROM multichain_wallets WHERE id=? AND enabled=1`, identity.WalletID).Scan(&chainID, &address); errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrWallet
		} else if err != nil {
			return Record{}, err
		}
		if !common.IsHexAddress(address) || common.HexToAddress(address) != common.HexToAddress(identity.WalletAddress) {
			return Record{}, ErrWallet
		}
		stamp := now().UTC()
		if stamp.IsZero() || stamp.Year() < 1970 || stamp.Year() > 9998 {
			return Record{}, ErrInvalid
		}
		expAt := stamp.Add(time.Duration(ttl) * time.Second)
		policyJSON := encode(policy)
		record := Record{
			RequestID:          "req_" + hash([]byte(identity.PrincipalID+"\x00"+key)),
			Status:             "REJECTED",
			Reason:             "SUBMISSION_DISABLED",
			Request:            r,
			APIPolicyVersion:   policy.Version,
			APIPolicyHash:      hash([]byte(policyJSON)),
			CreatedAt:          stamp.Format(time.RFC3339Nano),
			ExpiresAt:          expAt.Format(time.RFC3339Nano),
			DeadlineCapability: "CONTRACT_DEADLINE_UNIX",
			ContractDeadline:   true,
		}
		_, err = conn.ExecContext(ctx,
			`INSERT INTO multichain_http_trade_requests(id,principal_id,idempotency_key,chain_id,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,outcome,reason_code,deadline_capability,contract_deadline,send_authorized,submission_queued)
			 VALUES(?,?,?,?,?,?,?,'MANUAL',?,?,?,?,?,?,?,'REJECTED','SUBMISSION_DISABLED','CONTRACT_DEADLINE_UNIX',?,0,0)`,
			record.RequestID, identity.PrincipalID, key, chainID, r.Chain, r.WalletID, address, fingerprint, encoded,
			policy.Version, record.APIPolicyHash, policyJSON, record.ExpiresAt, record.CreatedAt, expAt.Unix(),
		)
		if err != nil {
			return Record{}, err
		}
		_, err = conn.ExecContext(ctx, "COMMIT")
		record.projectProgress()
		return record, err
	}
	var address string
	if err = conn.QueryRowContext(ctx, `SELECT address FROM dry_run_wallets WHERE id=? AND chain_id=4663 AND enabled=1`, identity.WalletID).Scan(&address); errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrWallet
	} else if err != nil {
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
	record := Record{RequestID: "req_" + hash([]byte(identity.PrincipalID+"\x00"+key)), Status: "REJECTED", Reason: "SUBMISSION_DISABLED", Request: r, APIPolicyVersion: policy.Version, APIPolicyHash: hash([]byte(policyJSON)), CreatedAt: stamp.Format(time.RFC3339Nano), ExpiresAt: stamp.Add(time.Duration(ttl) * time.Second).Format(time.RFC3339Nano), DeadlineCapability: "APPLICATION_TTL_ONLY"}
	_, err = conn.ExecContext(ctx, `INSERT INTO user_http_trade_requests(id,principal_id,idempotency_key,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,outcome,reason_code,deadline_capability,contract_deadline,send_authorized,submission_queued) VALUES(?,?,?,'robinhood',?,?,'MANUAL',?,?,?,?,?,?,?,'REJECTED','SUBMISSION_DISABLED','APPLICATION_TTL_ONLY',0,0,0)`, record.RequestID, identity.PrincipalID, key, r.WalletID, address, fingerprint, encoded, policy.Version, record.APIPolicyHash, policyJSON, record.ExpiresAt, record.CreatedAt)
	if err != nil {
		return Record{}, err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	record.projectProgress()
	return record, err
}

func (s *Store) Get(ctx context.Context, identity Identity, id string, byKey bool) (Record, error) {
	if s == nil || s.db == nil || ctx == nil || !identity.valid() || !validID(id) {
		return Record{}, ErrInvalid
	}
	field := "id"
	if byKey {
		field = "idempotency_key"
	}
	r, _, err := scanRecord(s.db.QueryRowContext(ctx, selectRecord+`WHERE principal_id=? AND wallet_id=? AND `+field+`=?`, identity.PrincipalID, identity.WalletID, id))
	if errors.Is(err, ErrNotFound) {
		r, _, err = scanManualRecord(s.db.QueryRowContext(ctx, selectManualRecord+`WHERE m.principal_id=? AND m.wallet_id=? AND m.`+field+`=?`, identity.PrincipalID, identity.WalletID, id))
	}
	if errors.Is(err, ErrNotFound) {
		r, _, err = scanRecord(s.db.QueryRowContext(ctx, selectMultiChainRecord+`WHERE principal_id=? AND wallet_id=? AND `+field+`=?`, identity.PrincipalID, identity.WalletID, id))
	}
	if err == nil && r.OperationID != nil {
		r.Admission, err = s.manualAdmissionHistory(ctx, identity, r.RequestID)
		if err != nil {
			return Record{}, err
		}
		v := &ManualValidator{store: s}
		resolution, p, digest, found, e := v.loadResolution(ctx, *r.OperationID)
		if e != nil {
			return Record{}, e
		}
		if found {
			result, exists, e := v.loadResult(ctx, *r.OperationID, resolution, p, digest, r.ExpiresAt)
			if e != nil {
				return Record{}, e
			}
			if exists {
				r.Validation = &result
			}
		}
		r.Execution, err = s.manualExecutionSnapshot(ctx, identity, r.RequestID)
		if err != nil {
			return Record{}, err
		}
	}
	if err == nil {
		r.projectProgress()
	}
	return r, err
}

type Event struct {
	Cursor    uint64 `json:"cursor"`
	Type      string `json:"type"`
	Reason    string `json:"reason_code"`
	CreatedAt string `json:"created_at"`
}

type Events struct {
	Scope      string  `json:"scope"`
	Events     []Event `json:"events"`
	NextCursor uint64  `json:"next_cursor"`
}

func (s *Store) Events(ctx context.Context, identity Identity, id string, after uint64) (Events, error) {
	result := Events{Scope: "API_REJECTION_ONLY", Events: []Event{}, NextCursor: after}
	if after > 1<<63-1 {
		return result, ErrInvalid
	}
	record, err := s.Get(ctx, identity, id, false)
	if err != nil {
		return result, err
	}
	var e Event
	query := `SELECT r.sequence,a.event_type,a.reason_code,a.created_at FROM user_http_trade_requests r JOIN canary_runtime_audit a ON a.id='user-http-audit:'||r.id WHERE r.id=? AND r.principal_id=? AND r.wallet_id=? AND r.sequence>?`
	if record.OperationID != nil {
		return s.manualLifecycleEvents(ctx, identity, id, after)
	}
	err = s.db.QueryRowContext(ctx, query, id, identity.PrincipalID, identity.WalletID, after).Scan(&e.Cursor, &e.Type, &e.Reason, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Events = append(result.Events, e)
	result.NextCursor = e.Cursor
	return result, nil
}

type Wallet struct {
	ID      string `json:"wallet_id"`
	Chain   string `json:"chain"`
	Address string `json:"address"`
}

func (s *Store) Wallet(ctx context.Context, identity Identity) (Wallet, error) {
	var w Wallet
	if s == nil || s.db == nil || ctx == nil || !identity.valid() {
		return w, ErrInvalid
	}
	w.ID, w.Chain = identity.WalletID, "robinhood"
	err := s.db.QueryRowContext(ctx, `SELECT address FROM dry_run_wallets WHERE id=? AND chain_id=4663 AND enabled=1`, identity.WalletID).Scan(&w.Address)
	if errors.Is(err, sql.ErrNoRows) {
		var cid uint64
		err = s.db.QueryRowContext(ctx, `SELECT chain_id, address FROM multichain_wallets WHERE id=? AND enabled=1`, identity.WalletID).Scan(&cid, &w.Address)
		if errors.Is(err, sql.ErrNoRows) {
			return w, ErrWallet
		}
		switch cid {
		case 56:
			w.Chain = "bsc"
		case 8453:
			w.Chain = "base"
		}
	}
	if err != nil {
		return w, err
	}
	if !common.IsHexAddress(w.Address) || common.HexToAddress(w.Address) != common.HexToAddress(identity.WalletAddress) {
		return w, ErrWallet
	}
	return w, nil
}

// An unresolved lane or retained MANUAL nonce claim makes token inventory
// unavailable for new execution. This slice does not support MANUAL wallet
// reuse after canonical settlement. Treating these as blockers, not balances
// to subtract, avoids double counting C07/execution reservations. No writes.
func (s *Store) readWalletState(ctx context.Context, identity Identity) (Wallet, bool, error) {
	w := Wallet{ID: identity.WalletID, Chain: "robinhood"}
	if s == nil || s.db == nil || ctx == nil || !identity.valid() {
		return w, false, ErrInvalid
	}
	var blocked bool
	err := s.db.QueryRowContext(ctx, `SELECT w.address,EXISTS(SELECT 1 FROM execution_wallet_lanes WHERE wallet_id=w.id AND state!='idle') OR EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=w.id AND status IN ('reserved','signed','frozen')) OR EXISTS(SELECT 1 FROM canary_risk_reservations WHERE wallet_id=w.id AND state IN ('reserved','frozen')) OR EXISTS(SELECT 1 FROM execution_steps WHERE wallet_id=w.id AND status IN ('nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution')) OR EXISTS(SELECT 1 FROM manual_nonce_claims WHERE wallet_id=w.id OR wallet_address=lower(w.address)) FROM dry_run_wallets w WHERE w.id=? AND w.chain_id=4663 AND w.enabled=1`, identity.WalletID).Scan(&w.Address, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, ErrWallet
	}
	if err == nil && (!common.IsHexAddress(w.Address) || common.HexToAddress(w.Address) != common.HexToAddress(identity.WalletAddress)) {
		return w, false, ErrWallet
	}
	return w, blocked, err
}
