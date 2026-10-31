# HTTP API P5 — controlled local submission worker

Status: `P5_PASS`

Formal closeout: `COMPLETE_LOCAL_ONLY`

The existing `ControlledSubmissionWorker` now has a focused review record. It
discovers durable `ISSUED` permits from SQLite, validates the independent
`SUBMISSION` lease, performs the immediate pre-send gate, consumes the exact
permit in the same transaction as the submission row, and sends only the
prepared exact artifact supplied by the controlled local harness.

The worker preserves the existing `Operation -> ExecutionStep ->
TransactionAttempt -> transaction_submissions` state owner. It does not create
an artifact, allocate a nonce, rebuild, resign, replace, fee-bump, or release a
`broadcast_unknown` lane. It distinguishes deterministic known-unsent from
the conservative ambiguity boundary marked by `send_intent_at`.

The test broadcaster is local-only. No production startup wiring was added;
the production graph still has no broadcaster, `SendRawTransaction`, or real
canary authorization path.

Validation on the controlled local harness:

```text
go test ./internal/trade -run '^TestControlledSubmission' -count=1 -timeout=20m
  PASS (4.076s)

go test -race ./internal/trade -run '^TestControlledSubmission' -count=1 -timeout=25m
  PASS (88.841s)
```

Covered cases include first broadcast exactly once, deterministic rejection,
ambiguous freeze and no second send, lease loss, exact-artifact unknown replay,
emergency stop before final gate, restart before/after send intent, artifact
failure, expired permit scan, lease takeover barrier, emergency-stop commit
window, and duplicate workers.

The required safety state remains:

```ini
P5 = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
