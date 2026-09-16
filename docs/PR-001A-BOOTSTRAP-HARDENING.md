# PR-001A Bootstrap Hardening

## Scope

This change turns the initial process skeleton into a reusable runtime base. It adds no Feed,
strategy, quote, signing or broadcast behavior. Live remains fail-closed.

## Startup contract

Each service:

1. validates configuration and chain identity;
2. rejects `RBH_LIVE_ENABLED=true`;
3. opens only its configured database;
4. enables WAL, foreign keys and a bounded busy timeout;
5. additionally enforces `synchronous=FULL` for trade-service;
6. acquires an exclusive migration lock and applies embedded owner-specific migrations;
7. binds its Unix socket without replacing a non-socket path;
8. reports ready only after every gate passes.

The readiness gates are `config_valid`, `database_open`, `migrations_current`, `socket_bound`
and `live_disabled`. `/health` only reports process liveness.

## IPC base

The shared IPC package supplies a Unix socket HTTP client plus an authentication framework with
HMAC-SHA256, body hash, timestamp window, request nonce replay cache, request ID propagation and
structured errors. No business endpoint is exposed by this PR. A unique secret of at least 32
bytes is required before a future internal business API enables the middleware.

## Evidence

- Go tests, race tests and vet pass under Go 1.26.6.
- staticcheck passes.
- Migration tests cover all three owners, idempotent replay and database lock failure.
- Socket tests cover readiness transitions, graceful shutdown and refusal to replace files.
- Authentication tests cover valid requests, body mutation and nonce replay.
- Phase 0 C01 evidence remains separately scoped as `PASS_OFFLINE_ONLY`.

