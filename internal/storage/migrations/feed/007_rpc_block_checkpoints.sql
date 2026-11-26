-- RPC block heights are independent of Nitro sequencer message numbers.
CREATE TABLE feed_rpc_checkpoints(
 chain_id INTEGER PRIMARY KEY,
 block_number INTEGER NOT NULL CHECK(block_number >= 0),
 block_hash TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE feed_rpc_source_audit(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 chain_id INTEGER NOT NULL,
 kind TEXT NOT NULL,
 from_block INTEGER,
 to_block INTEGER NOT NULL,
 detail TEXT NOT NULL,
 created_at TEXT NOT NULL
);
