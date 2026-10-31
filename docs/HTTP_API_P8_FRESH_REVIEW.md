# HTTP API P8 fresh independent review

Conclusion: `PASS` (scope limited to controlled canary authorization harness
and authorization boundary)

Reviewed branch: `http-p8-canary-authorization-scope`
Reviewed head: `d4f78af`

The review verified that the durable authorization row epoch is immutable.
Revocation and expiry invalidate an authorization by durable state, while a
higher global epoch is assigned only when a new replacement authorization is
created. This matches the schema and Store API.

The review also verified chain/wallet/protocol/contract/token/policy and
deployment binding, atomic budget conservation, duplicate/restart behavior,
emergency-stop fail-closed priority, readiness separation,
`broadcast_unknown` freeze, recovery continuity, and the disabled production
composition. No HTTP authorization grant route, production broadcaster,
mainnet, BSC/Flap, or live path is reachable.

Controlled harness evidence remains:

```text
ordinary = PASS (67.066s)
race = PASS (1053.703s)
```

This PASS does not grant authorization. Formal closeout is pending.

```ini
P8 = PASS_REVIEW_ONLY
P8_formal_closeout = PENDING
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
live = false
release_ready = false
```
