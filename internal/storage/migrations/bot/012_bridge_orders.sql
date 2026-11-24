CREATE TABLE IF NOT EXISTS bridge_orders (
    order_id TEXT PRIMARY KEY,
    chat_id INTEGER NOT NULL,
    from_symbol TEXT NOT NULL,
    to_symbol TEXT NOT NULL,
    amount_str TEXT NOT NULL,
    receive_address TEXT NOT NULL,
    anonymous INTEGER NOT NULL DEFAULT 0 CHECK (anonymous IN (0, 1)),
    deposit_address TEXT NOT NULL DEFAULT '',
    expected_out TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'QUOTED',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
