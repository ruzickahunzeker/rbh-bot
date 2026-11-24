CREATE TABLE IF NOT EXISTS tg_pending_inputs (
    chat_id INTEGER PRIMARY KEY,
    action TEXT NOT NULL,
    target_key TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
