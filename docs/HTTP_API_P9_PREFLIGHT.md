# HTTP API P9 controlled canary preflight

Status: `P9_PASS`

Formal closeout: `COMPLETE_LOCAL_ONLY`

P9 preflight is a read-only evidence package. It verifies the final reviewed
composition and durable controls without creating an authorization record,
arming a canary, connecting a broadcaster, or sending a transaction.

Validation:

```text
go test ./internal/config ./internal/app ./internal/trade ./internal/tradeapi \
  -run 'TestConfig|TestTradeAPIQueryOnly|TestUserTradeAPI|TestShutdown|TestW4A|TestW4C|TestControlledSubmission|TestHTTPC3B4TenThousand|TestHTTPC3B3Atomic|TestHTTPM4B23' \
  -count=1 -timeout=35m
  PASS (config; app 0.046s; trade 21.043s; tradeapi 47.409s)

go vet ./...          PASS
staticcheck ./...     PASS
gofmt tracked Go      PASS
```

The preflight scan confirms the runtime default is `DISABLED`, the canary mode
rejects non-disabled configuration, no HTTP authorization grant route exists,
and production recovery remains read-only. Local test broadcasters and
SubmissionService references are harness-only and are not constructed by
production composition.

The proposed profile remains Robinhood/4663 + Pons v2 Curve with the reviewed
wallet/contract/token/policy identities. The operator approval artifact is not
present, so no authorization can be granted by this package.

```ini
P8 = PASS
P9 = READY_FOR_REVIEW
preflight = NOT_AUTHORIZED
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
