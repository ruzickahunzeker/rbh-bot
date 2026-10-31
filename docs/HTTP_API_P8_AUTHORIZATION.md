# HTTP API P8 — controlled authorization harness

Status: `P8_READY_FOR_REVIEW`

P8 scope and controlled harness evidence are complete. The harness verifies
durable authorization identity, immutable epoch and revoke/expiry state
behavior, C04/C05/C07/C08 gate
binding, atomic max-operation and total-input budget accounting, emergency-stop
priority, duplicate/restart behavior, and permit/ambiguity boundaries.

The harness does not grant a real authorization, connect a production
broadcaster, call mainnet, or enable live. Authorization remains an explicit
operator action outside the HTTP order path. Readiness and recovery behavior
remain independent, and a `broadcast_unknown` lane/reservation stays frozen
through stop, TTL, revoke, and shutdown.

Validation:

```text
go test ./internal/tradeapi \
  -run 'TestHTTPC3B3Atomic|TestHTTPC3B4TenThousand|TestHTTPM1.*(Stop|Expiry|Restart)|TestHTTPM4B22.*(Stop|Restart)|TestHTTPM4B23' \
  -count=1 -timeout=30m
  PASS (67.066s)

go test -race ./internal/tradeapi \
  -run 'TestHTTPC3B3Atomic|TestHTTPC3B4TenThousand|TestHTTPM1.*(Stop|Expiry|Restart)|TestHTTPM4B22.*(Stop|Restart)|TestHTTPM4B23' \
  -count=1 -timeout=40m
  PASS (1053.703s)
```

The controlled profile remains Robinhood/4663 and the reviewed Pons v2 Curve
allowlist. BSC/Flap, Base, Pons v4, Long, LP, Solana, and unrestricted live are
not included.

```ini
P7 = PASS
P8 = READY_FOR_REVIEW
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
