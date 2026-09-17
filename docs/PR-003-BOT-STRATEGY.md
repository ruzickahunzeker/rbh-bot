# PR-003 Bot Strategy

Baseline: `main@9b597366bae62f5a17f90de64369e8c31d08a1fe`.

## Implemented boundary

```text
feed durable outbox
  -> authenticated Unix-socket Bot consumer
  -> bot.db signal inbox
  -> enabled watched-wallet and strategy match
  -> explicit copy policy
  -> deterministic operation intent
  -> atomic bot progress commit
```

The Feed database remains owned by `feed-service`; `bot-service` never opens it. A feed event,
policy decision, operation intent and `bot_progress.durable_event_offset` are committed in one Bot
SQLite transaction. Fetch-before-commit crashes replay safely, committed offsets resume strictly
after the last durable event, and gaps fail closed.

Operation intent identity is derived from the strategy ID, immutable strategy version, source
observation ID and copy direction. The intent stores the complete policy snapshot used for the
decision. Same-version strategy mutation and version rollback are rejected.

Sequencer retractions mark any still-created intent for the original observation as `retracted`.
Both the original inbox row and the compensating inbox row remain durable, and replaying either
event is idempotent.

## Explicit exclusions

- quote
- simulation
- nonce allocation
- signing
- broadcast

An operation intent is a durable request candidate only. It is not an admitted or executable
trade and cannot cause a chain-side effect in this slice.
