#!/usr/bin/env bash
# Online SQLite VACUUM INTO hot backup for BSC, RBH, and bot-service via local 0600 admin UDS sockets.
set -euo pipefail

BACKUP_ROOT="${1:-/root/evm-bot/data/backups/$(date -u +%Y%m%d-%H%M%S)}"
mkdir -p "${BACKUP_ROOT}"
chmod 700 "${BACKUP_ROOT}"

backup_one() {
  local name="$1" sock="$2"
  if [[ -S "${sock}" ]]; then
    /root/evm-bot/bin/operator admin backup --socket "${sock}" --dest "${BACKUP_ROOT}/${name}.db"
    chmod 600 "${BACKUP_ROOT}/${name}.db"
    echo "[OK] backed up ${name} -> ${BACKUP_ROOT}/${name}.db"
  else
    echo "[SKIP] ${name} socket not found: ${sock}"
  fi
}

backup_one "trade-bsc"   "/root/evm-bot/data/bsc/run/trade-admin.sock"
backup_one "feed-bsc"    "/root/evm-bot/data/bsc/run/feed-admin.sock"
backup_one "trade-rbh"   "/root/evm-bot/data/rbh/run/trade-admin.sock"
backup_one "feed-rbh"    "/root/evm-bot/data/rbh/run/feed-admin.sock"
backup_one "bot-service" "/root/evm-bot/data/bot/run/bot-admin.sock"
