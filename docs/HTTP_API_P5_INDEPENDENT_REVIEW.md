# HTTP API P5 controlled local submission independent review

Conclusion: `PASS` (scope limited to the controlled local submission worker)

Reviewed branch: `http-p5-controlled-local-submission`
Reviewed head: `ada1d96`

The review verified the following:

- `SUBMISSION` lease fencing rejects stale owners and serializes takeover;
- `FIRST_BROADCAST` and `UNKNOWN_REPLAY` are distinct one-shot permits;
- immediate pre-send control/lease/TTL/reservation checks and exact permit
  consumption are committed with the `submitting` row;
- `send_intent_at` is the durable ambiguity boundary;
- deterministic rejection becomes `known_unsent` only when non-propagation is
  proven;
- post-marker, in-RPC, and pre-outcome uncertainty becomes
  `broadcast_unknown` and retains frozen lane/reservation;
- restart, expired permit, artifact failure, stop race, lease race, and
  duplicate-worker cases are covered;
- unknown replay is query-first and uses the exact original artifact;
- no rebuild, resign, replacement, fee bump, or new nonce is reachable;
- production startup has no P5 worker/broadcaster wiring and remains disabled.

Evidence:

```text
go test ./internal/trade -run '^TestControlledSubmission' -count=1 -timeout=20m
  PASS (4.076s)
go test -race ./internal/trade -run '^TestControlledSubmission' -count=1 -timeout=25m
  PASS (88.841s)
go vet ./...
  PASS
gofmt tracked Go files
  PASS
```

The reviewer used committed source and evidence; the review environment could
not rerun Go commands because its `go` executable was unavailable. This PASS
does not authorize production broadcast, canary authorization, live, or P6.

```ini
P5 = PASS_REVIEW_ONLY
P5_formal_closeout = PENDING
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```
