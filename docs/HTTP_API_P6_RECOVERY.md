# HTTP API P6 — recovery lifecycle composition

Status: `P6_PASS`

Formal closeout: `COMPLETE_LOCAL_ONLY`

P6 verifies the production lifecycle composition of the existing controlled
recovery worker. It uses the durable `RECOVERY` lease and the existing
Operation -> ExecutionStep -> TransactionAttempt recovery state owner. Startup
discovers durable submitted and broadcast-unknown work from SQLite; it does not
depend on an in-memory queue.

Recovery readiness is independent from admission and submission readiness. It
can query receipts, canonical headers, and reorg evidence while emergency stop,
authorization, TTL, or deployment drift blocks new economic actions. Lease
loss, RPC health failure, contradictory evidence, and shutdown drain set
recovery readiness false and record durable audit/alert evidence. Shutdown waits
for recovery drain before closing resources; restart can rediscover unfinished
work.

The recovery dependency graph contains no signer, SubmissionService,
RawBroadcaster, `SendRawTransaction`, permit issuance/consumption, or nonce
allocation. Submission and recovery leases remain independent. P6 does not
change P5 send-intent or broadcast-unknown semantics.

Validation:

```text
go test ./internal/trade ./internal/app \
  -run 'TestW4C|TestControlledRecovery|TestShutdown|TestRecovery|TestW4A' \
  -count=1 -timeout=25m
  PASS (trade 15.168s; app 0.052s)

go test -race ./internal/trade ./internal/app \
  -run 'TestW4C|TestControlledRecovery|TestShutdown|TestRecovery|TestW4A' \
  -count=1 -timeout=30m
  PASS (trade 368.338s; app 1.215s)
```

The production lifecycle remains disabled for economic actions:

```ini
P5 = PASS
P6 = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
