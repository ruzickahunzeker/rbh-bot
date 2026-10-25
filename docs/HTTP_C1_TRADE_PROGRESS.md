# HTTP-C1 trade progress projection

Branch: `http-c1-trade-progress`, based on `main@404bbff12826ba8bd6c6552512704b28fb0c9d96`.
Review ceiling: `HTTP_C1_READY_FOR_REVIEW`; this document does not claim
independent PASS, submission readiness or live authorization.

The existing authenticated `POST /v1/trades`, `GET /v1/trades/{request_id}` and
`GET /v1/trades/by-idempotency-key` now return `progress.stage`. The stage is
derived only from already verified durable HTTP intake, C2 validation and C3
admission observations. It is not a new lifecycle state or permission source.

| Stage | Evidence | Not implied |
|---|---|---|
| `REJECTED` | Stored HTTP rejection | New request, admission or send |
| `INTAKE_RECORDED` | Immutable manual order source | Validated/tradable token or risk reservation |
| `VALIDATION_RECORDED` | Verified saved validation result | Admission, signing or submission |
| `ADMISSION_RECORDED` | Verified saved admission history | Current authorization, signing, sending or receipt |

The top-level `status` and `reason_code` retain their historical meaning;
`RECORDED / VALIDATION_NOT_CONNECTED` still describes initial intake, even if
later validation/admission is visible separately. No client should infer a
transaction result from those fields. `ADMISSION_RECORDED` is historical on
GET and duplicate POST, irrespective of current stop, authorization or TTL.

This C1 slice makes no new database writes, queue entries, attempts, permits,
nonce reservations, signing or broadcaster calls. It does not change the
authentication scope or default-off configuration. Missing/corrupt durable
validation or admission evidence is not promoted to a higher stage; existing
verified readers preserve their fail-closed error behavior.

The next A integration must expose durable attempt/submission identity and
outcome before HTTP can represent signing or send. The B recovery integration
must expose verified receipt/canonical/reorg observations before HTTP can
represent settlement. Those later stages require separate contract, tests and
review; this stage does not predeclare them or authorize live trading.
