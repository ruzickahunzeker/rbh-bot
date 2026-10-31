# HTTP API P4 — controlled exact-artifact signing

Status: `P4_READY_FOR_REVIEW`

P4 verifies the existing controlled local signing path that follows durable
admission. It binds the original request, source, policy, gate snapshot,
nonce claim, signer input, and signed artifact byte identity. Pre-sign and
final control checks fail closed on stop, revoke, expiry, lease loss, source
drift, or evidence corruption. Faults retain ambiguity and never rebuild,
resign, replace, fee-bump, or allocate a second nonce.

This is a local harness only. It has no HTTP production startup wiring and no
SubmissionService, RawBroadcaster, `SendRawTransaction`, mainnet, or live
authorization dependency.

Validation:

```text
go test ./internal/tradeapi -run '^TestHTTPM3B3' -count=1 -timeout=25m
  PASS (95.753s)

go test -race ./internal/tradeapi -run '^TestHTTPM3B3C' -count=1 -timeout=15m
  PASS (687.159s)

go test -race ./internal/tradeapi -run '^TestHTTPM3B3D' -count=1 -timeout=15m
  PASS (420.272s)

go test -race ./internal/tradeapi \
  -run '^TestHTTPM3B3DImmediateCallbackAfterSignerChecks$' \
  -count=1 -timeout=5m
  PASS (13.131s)
```

The combined race invocation completed successfully after the extended timeout
was raised to 45 minutes. The earlier 25-minute timeout remains historical
evidence and is not used as a current failure.

```text
go test -race ./internal/tradeapi -run '^TestHTTPM3B3' -count=1 -timeout=45m
  PASS (2313.955s)
```

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
