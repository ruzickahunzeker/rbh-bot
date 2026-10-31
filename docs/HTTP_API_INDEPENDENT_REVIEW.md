# HTTP API P0–P4 independent review

Conclusion: `BLOCKED`

Reviewed branch: `http-api-p0-p4-review-gates`
Reviewed head: `13b711ecb55c101b94e499f081d454a4fedbaf20`

The review found no evidence of broadcaster, raw-send, live authorization, or
an HTTP route that exposes the local signer/submission methods. P1 intake, P2
read-only query, P3 admission, and the bounded P4 test groups are materially
covered. P0–P4 cannot receive a PASS until the following blockers are closed.

## Blocker 1 — full P4 race evidence is incomplete

The combined command
`go test -race ./internal/tradeapi -run '^TestHTTPM3B3'` timed out at 25
minutes while running `TestHTTPM3B3DImmediateCallbackAfterSignerChecks`.
The isolated test and the bounded C/D groups pass, but those results do not
prove the full P4 race acceptance. The timeout is already recorded in
`docs/HTTP_API_P4_SIGNING.md` and `docs/HTTP_API_PRE_REVIEW.md` and must remain
visible until the full-race qualification is resolved or the acceptance scope
is explicitly revised by a new review.

## Blocker 2 — production composition contradicts P2 query independence

`docs/HTTP_API_P2_QUERY.md` describes a read-only query path that does not
require signer or active authorization. However,
`internal/app/run.go` validates RPC, dry-run wallet/from-address, execution
private key, and artifact encryption configuration before calling
`configureUserTradeAPI`. As a result, a production process cannot start the
HTTP query surface with only query dependencies. The route remains on the
existing UDS and does not expose send methods, but the composition contract is
not yet true. The fix must either split query composition from signer
requirements or revise the P2 contract and evidence; it must not wire a
broadcaster or enable live.

Required state while blocked:

```ini
P0_P4 = BLOCKED_PENDING_HARDENING
P5_submission = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```
