# HTTP API P8 controlled authorization independent review

Initial conclusion: `BLOCKED_PENDING_SCOPE_CORRECTION`

Reviewed branch: `http-p8-canary-authorization-scope`
Reviewed head: `fb9a012`

The review found one documentation/implementation mismatch. The original P8
scope said revoke or expiry increments the authorization epoch. The durable
schema makes the authorization row epoch immutable, and the transition writer
changes state and audit only. Epochs advance when a new authorization is
created. Existing revoked/expired rows still fail closed by state.

The scope and harness documents were corrected to state the implemented
semantics: immutable row epoch, state-based invalidation, and a higher global
epoch for a later replacement authorization. A fresh independent review is
required after that correction. No production code, authorization grant,
broadcaster, or live path was changed.

```ini
P8 = READY_FOR_FRESH_REVIEW
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
live = false
release_ready = false
```
