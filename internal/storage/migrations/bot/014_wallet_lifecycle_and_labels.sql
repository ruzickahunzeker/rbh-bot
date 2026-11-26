-- Preserve identity/history while allowing an explicitly deleted address to be imported under a new ID.
ALTER TABLE evm_wallets RENAME TO _evm_wallets_m35;
CREATE TABLE evm_wallets (
 wallet_id TEXT PRIMARY KEY,
 address TEXT NOT NULL,
 wallet_type TEXT NOT NULL CHECK(wallet_type IN ('keystore','readonly')),
 keystore_path TEXT NOT NULL DEFAULT '',
 owner_chat_id INTEGER NOT NULL DEFAULT 0,
 legacy_chain_id INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'ACTIVE' CHECK(state IN ('ACTIVE','DELETING','DELETED')),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO evm_wallets(wallet_id,address,wallet_type,keystore_path,owner_chat_id,legacy_chain_id,created_at,updated_at)
 SELECT wallet_id,address,wallet_type,keystore_path,owner_chat_id,legacy_chain_id,created_at,updated_at FROM _evm_wallets_m35;
DROP TABLE _evm_wallets_m35;
CREATE UNIQUE INDEX evm_wallet_active_address ON evm_wallets(lower(address)) WHERE state != 'DELETED';
CREATE INDEX idx_evm_wallets_owner ON evm_wallets(owner_chat_id);
CREATE TABLE evm_wallet_labels (
 chat_id INTEGER NOT NULL,
 wallet_id TEXT NOT NULL REFERENCES evm_wallets(wallet_id),
 label TEXT NOT NULL CHECK(length(label) <= 32),
 updated_at TEXT NOT NULL,
 PRIMARY KEY(chat_id,wallet_id)
);

-- Enforce at the SQL write boundary, including direct worker writes and re-enabling tasks.
-- Legacy unknown IDs keep their existing behavior; retained lifecycle rows prevent reuse of deleted IDs.
CREATE TRIGGER wallet_conditional_insert BEFORE INSERT ON conditional_orders
 WHEN NEW.state IN ('ACTIVE','TRIGGERED') AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_conditional_update BEFORE UPDATE ON conditional_orders
 WHEN NEW.state IN ('ACTIVE','TRIGGERED') AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_sniper_insert BEFORE INSERT ON sniper_tasks
 WHEN NEW.state IN ('ARMED','TRIGGERED') AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_sniper_update BEFORE UPDATE ON sniper_tasks
 WHEN NEW.state IN ('ARMED','TRIGGERED') AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_copy_insert BEFORE INSERT ON copy_wallet_profiles
 WHEN NEW.enabled=1 AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_copy_update BEFORE UPDATE ON copy_wallet_profiles
 WHEN NEW.enabled=1 AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_submission_insert BEFORE INSERT ON submission_jobs
 WHEN NEW.state NOT IN ('COMPLETED','EXPIRED_SKIPPED','FAILED_TERMINAL') AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
CREATE TRIGGER wallet_submission_update BEFORE UPDATE ON submission_jobs
 WHEN NEW.state NOT IN ('COMPLETED','EXPIRED_SKIPPED','FAILED_TERMINAL') AND EXISTS(SELECT 1 FROM evm_wallets WHERE wallet_id=NEW.wallet_id AND state!='ACTIVE')
 BEGIN SELECT RAISE(ABORT,'WALLET_NOT_ACTIVE'); END;
