ALTER TABLE bloom_user_settings ADD COLUMN auto_tpsl INTEGER NOT NULL DEFAULT 0 CHECK (auto_tpsl IN (0, 1));
