# HTTP API P3 — controlled admission and risk reservation

Status: `P3_READY_FOR_REVIEW`

P3 connects the HTTP manual intake to the existing durable admission and risk
ledgers through the existing `Operation -> ExecutionStep` owner. It records
validated source identity, C04/C05/C07/C08 gate attestations, policy identity,
wallet lane, risk reservation, authorization usage, immutable expiry, audit,
and alert outbox in the existing transaction model.

The admission layer does not sign, issue or consume a send permit, allocate a
nonce, call SubmissionService, call a broadcaster, or authorize live execution.
Emergency stop is evaluated first. Missing, stale, contradictory, or
unverifiable control data fails closed. Duplicate requests retrieve historical
state and do not consume budget twice. Restart preserves durable usage and
reservation state. A frozen or ambiguous lane remains frozen.

Validation evidence on the controlled local SQLite fixtures:

```text
go test ./internal/tradeapi -run '^TestHTTPC3B' -count=1 -timeout=20m       PASS (51.176s)
go test -race ./internal/tradeapi -run '^TestHTTPC3B' -count=1 -timeout=20m PASS (849.232s)
```

The race run includes atomic admission fault windows, two-handle contests,
control-writer commit fencing, duplicate/restart behavior, emergency-stop
priority, authorization budget accounting, and the 10,000 mixed conservation
case. Independent review is still required before P3 is recorded as PASS.

The required safety state remains:

```ini
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
