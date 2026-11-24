-- Step 1B (R6): Normalized wallet uniqueness, multi-generation Reorg-safe reservation index,
-- serial wallet reuse guard, and pre-sign abort with bidirectional signing_intent mutual exclusion.

-- 1. Enforce 1:1 (chain_id, lower(address)) uniqueness on registered wallets.
CREATE UNIQUE INDEX IF NOT EXISTS one_wallet_per_chain_normalized_address
ON dry_run_wallets(chain_id, lower(address));

-- 2. Upgrade 007 unique reservation index:
-- At most ONE active 'reserved' reservation per wallet, while allowing multiple historical
-- generations to roll back to 'frozen' simultaneously during a multi-block Reorg (AT-05).
DROP INDEX IF EXISTS one_active_canary_reservation_per_wallet;
CREATE UNIQUE INDEX one_active_canary_reservation_per_wallet
ON canary_risk_reservations(wallet_id)
WHERE state = 'reserved';

-- 3. Immutable pre-sign abort audit table for provably unsigned expired/crashed admissions.
CREATE TABLE manual_pre_send_aborts(
  operation_id TEXT PRIMARY KEY REFERENCES operations(id),
  reservation_id TEXT NOT NULL,
  reason TEXT NOT NULL CHECK(length(trim(reason)) > 0),
  aborted_at TEXT NOT NULL
);
CREATE TRIGGER manual_pre_send_aborts_no_update
BEFORE UPDATE ON manual_pre_send_aborts
BEGIN
  SELECT RAISE(ABORT, 'MANUAL_PRE_SEND_ABORT_IMMUTABLE');
END;
CREATE TRIGGER manual_pre_send_aborts_no_delete
BEFORE DELETE ON manual_pre_send_aborts
BEGIN
  SELECT RAISE(ABORT, 'MANUAL_PRE_SEND_ABORT_IMMUTABLE');
END;

-- 4. Bidirectional SQLite trigger mutual exclusion between manual_pre_send_aborts
-- and manual_signing_intents (closes stalled-continuation vs abort-pre-sign race, AT-02).
CREATE TRIGGER manual_pre_send_abort_forbids_signing_intent
BEFORE INSERT ON manual_pre_send_aborts
WHEN EXISTS(SELECT 1 FROM manual_signing_intents WHERE operation_id = NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT, 'MANUAL_PRE_SEND_ABORT_CONFLICTS_WITH_SIGNING_INTENT');
END;

CREATE TRIGGER manual_signing_intent_forbids_pre_send_abort
BEFORE INSERT ON manual_signing_intents
WHEN EXISTS(SELECT 1 FROM manual_pre_send_aborts WHERE operation_id = NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT, 'MANUAL_SIGNING_INTENT_CONFLICTS_WITH_PRE_SEND_ABORT');
END;

-- 5. Immutable finality settlement log: marks generations that reached ExposureSettlementLevel
-- so unfinalized_native_outlay_wei can safely release their native exposure.
CREATE TABLE wallet_exposure_finality_settlements(
  operation_id TEXT PRIMARY KEY REFERENCES operations(id),
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  wallet_address TEXT NOT NULL CHECK(length(trim(wallet_address)) > 0),
  finalized_block_number INTEGER NOT NULL CHECK(finalized_block_number > 0),
  finalized_block_hash TEXT NOT NULL CHECK(length(trim(finalized_block_hash)) > 0),
  settled_at TEXT NOT NULL
);
CREATE TRIGGER wallet_exposure_finality_no_update
BEFORE UPDATE ON wallet_exposure_finality_settlements
BEGIN
  SELECT RAISE(ABORT, 'WALLET_EXPOSURE_FINALITY_IMMUTABLE');
END;
CREATE TRIGGER wallet_exposure_finality_no_delete
BEFORE DELETE ON wallet_exposure_finality_settlements
BEGIN
  SELECT RAISE(ABORT, 'WALLET_EXPOSURE_FINALITY_IMMUTABLE');
END;

-- 6. Upgrade 045 manual_recovery_no_wallet_reuse trigger to serial wallet reuse guard:
-- Allows next generation once all prior generations on the wallet (by wallet_id or lower(address))
-- have NO active 'reserved' or 'frozen' risk reservations and every prior claimed nonce has
-- reached canonical 'committed' or provably unsigned 'aborted_pre_sign' ('released').
DROP TRIGGER IF EXISTS manual_recovery_no_wallet_reuse;
CREATE TRIGGER manual_wallet_reuse_serial_guard
BEFORE INSERT ON manual_admission_commands
WHEN EXISTS(
  SELECT 1 FROM canary_risk_reservations r
  LEFT JOIN dry_run_wallets w ON w.id = r.wallet_id
  WHERE r.operation_id != NEW.operation_id
    AND r.state IN ('reserved', 'frozen')
    AND (
      r.wallet_id = json_extract(NEW.contract_json, '$.wallet_id')
      OR lower(COALESCE(w.address, '')) = lower(COALESCE(json_extract(NEW.contract_json, '$.wallet_address'), ''))
    )
)
OR EXISTS(
  SELECT 1 FROM manual_retained_wallet_claims retained
  LEFT JOIN canary_risk_reservations r ON r.operation_id = retained.operation_id
  WHERE retained.operation_id != NEW.operation_id
    AND (
      retained.wallet_id = json_extract(NEW.contract_json, '$.wallet_id')
      OR lower(retained.wallet_address) = lower(COALESCE(json_extract(NEW.contract_json, '$.wallet_address'), ''))
    )
    AND COALESCE(r.state, 'reserved') NOT IN ('committed', 'released')
)
BEGIN
  SELECT RAISE(ABORT, 'WALLET_IN_FLIGHT_OR_FROZEN');
END;
