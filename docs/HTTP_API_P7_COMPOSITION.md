# HTTP API P7 — production composition implementation

Status: `P7_PASS`

Formal closeout: `COMPLETE_LOCAL_ONLY`

P7 adds an explicit query-only composition boundary to the existing
trade-service startup. When the API is configured for read-only queries with
no manual intake, validation, or admission, startup requires only RPC and the
durable wallet scope. It does not require an execution private key, artifact
encryption key, signer, or production broadcaster. The authenticated API stays
on the existing local UDS server.

Execution-enabled configurations retain the prior execution-kernel,
artifact, recovery, and dry-run prerequisites. No existing send or recovery
state owner was changed. Submission and recovery worker leases remain
independent, and no P5 worker or broadcaster is wired into production startup
by this slice.

Validation:

```text
go test ./internal/app -run 'TestTradeAPIQueryOnly|TestUserTradeAPI|TestShutdown' -count=1 -timeout=10m
  PASS (0.208s)
go test -race ./internal/app -run 'TestTradeAPIQueryOnly|TestUserTradeAPI|TestShutdown' -count=1 -timeout=10m
  PASS (5.659s)
go test ./internal/trade -run 'TestW4A|TestW4C|TestControlledSubmission' -count=1 -timeout=20m
  PASS (14.328s)
go vet ./...
  PASS
staticcheck ./...
  PASS
gofmt tracked Go files
  PASS
```

The composition remains disabled for economic actions:

```ini
P5 = PASS
P6 = PASS
P7 = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
