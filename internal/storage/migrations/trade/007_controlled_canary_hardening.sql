INSERT INTO canary_gate_attestations(gate,status,evidence_hash,attested_at) VALUES
('C04','PASS','c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd','2026-09-27T00:00:00Z'),
('C05','PASS','1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b','2026-09-27T00:00:00Z'),
('C08','PASS','8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8','2026-09-27T00:00:00Z')
ON CONFLICT(gate) DO UPDATE SET status=excluded.status,evidence_hash=excluded.evidence_hash,attested_at=excluded.attested_at;

CREATE TRIGGER canary_gate_attestation_no_update BEFORE UPDATE ON canary_gate_attestations BEGIN SELECT RAISE(ABORT,'canary gate attestation is immutable'); END;
CREATE TRIGGER canary_gate_attestation_no_delete BEFORE DELETE ON canary_gate_attestations BEGIN SELECT RAISE(ABORT,'canary gate attestation is immutable'); END;
CREATE TRIGGER canary_policy_no_update BEFORE UPDATE ON canary_policies BEGIN SELECT RAISE(ABORT,'canary policy version is immutable'); END;
CREATE TRIGGER canary_policy_no_delete BEFORE DELETE ON canary_policies BEGIN SELECT RAISE(ABORT,'canary policy version is immutable'); END;
CREATE TRIGGER canary_wallet_allowlist_no_update BEFORE UPDATE ON canary_wallet_allowlist BEGIN SELECT RAISE(ABORT,'canary wallet allowlist version is immutable'); END;
CREATE TRIGGER canary_wallet_allowlist_no_delete BEFORE DELETE ON canary_wallet_allowlist BEGIN SELECT RAISE(ABORT,'canary wallet allowlist version is immutable'); END;
CREATE TRIGGER canary_contract_allowlist_no_update BEFORE UPDATE ON canary_contract_allowlist BEGIN SELECT RAISE(ABORT,'canary contract allowlist version is immutable'); END;
CREATE TRIGGER canary_contract_allowlist_no_delete BEFORE DELETE ON canary_contract_allowlist BEGIN SELECT RAISE(ABORT,'canary contract allowlist version is immutable'); END;
CREATE TRIGGER canary_token_allowlist_no_update BEFORE UPDATE ON canary_token_allowlist BEGIN SELECT RAISE(ABORT,'canary token allowlist version is immutable'); END;
CREATE TRIGGER canary_token_allowlist_no_delete BEFORE DELETE ON canary_token_allowlist BEGIN SELECT RAISE(ABORT,'canary token allowlist version is immutable'); END;

CREATE TABLE canary_admission_sources(
  operation_id TEXT PRIMARY KEY REFERENCES operations(id),
  wallet_id TEXT NOT NULL,
  wallet_address TEXT NOT NULL CHECK(length(wallet_address)=42),
  protocol TEXT NOT NULL CHECK(protocol='PONS_V2_CURVE'),
  direction TEXT NOT NULL CHECK(direction IN ('BUY','SELL')),
  token_address TEXT NOT NULL CHECK(length(token_address)=42),
  contract_address TEXT NOT NULL CHECK(length(contract_address)=42),
  contract_role TEXT NOT NULL,
  runtime_code_hash TEXT NOT NULL CHECK(length(runtime_code_hash)=66),
  amount TEXT NOT NULL,
  gas_limit TEXT NOT NULL,
  gas_fee_cap TEXT NOT NULL,
  gas_tip_cap TEXT NOT NULL,
  native_balance TEXT NOT NULL,
  slippage_bps INTEGER NOT NULL CHECK(slippage_bps BETWEEN 0 AND 10000),
  sell_bps INTEGER NOT NULL CHECK(sell_bps BETWEEN 0 AND 10000),
  policy_version INTEGER NOT NULL REFERENCES canary_policies(version),
  quote_block_number INTEGER NOT NULL CHECK(quote_block_number>0),
  quote_block_hash TEXT NOT NULL CHECK(length(quote_block_hash)=66),
  expires_at TEXT NOT NULL,
  source_hash TEXT NOT NULL CHECK(length(source_hash)=64),
  created_at TEXT NOT NULL
);
CREATE TRIGGER canary_source_no_update BEFORE UPDATE ON canary_admission_sources BEGIN SELECT RAISE(ABORT,'canary admission source is immutable'); END;
CREATE TRIGGER canary_source_no_delete BEFORE DELETE ON canary_admission_sources BEGIN SELECT RAISE(ABORT,'canary admission source is immutable'); END;

ALTER TABLE canary_risk_reservations ADD COLUMN quote_block_number INTEGER;
ALTER TABLE canary_risk_reservations ADD COLUMN quote_block_hash TEXT;
ALTER TABLE canary_risk_reservations ADD COLUMN source_hash TEXT;

CREATE UNIQUE INDEX one_active_canary_reservation_per_wallet
ON canary_risk_reservations(wallet_id)
WHERE state IN ('reserved','frozen');

CREATE TABLE canary_admission_attempts(
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  decision_id TEXT NOT NULL REFERENCES canary_admission_decisions(id),
  operation_id TEXT NOT NULL,
  outcome TEXT NOT NULL CHECK(outcome IN ('ADMITTED','DEDUPED','QUEUED','REJECTED')),
  reason_code TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX canary_admission_attempts_outcome_idx ON canary_admission_attempts(outcome);
