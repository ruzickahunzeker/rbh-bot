# A/B/C design reconciliation — author checkpoint, not independent approval

Common code baseline: local `80ad4e474c3016f8686dd9d9ca096302ae2cf10a`.
Author checkpoint: `ABC_DESIGN_RECONCILED_PENDING_REVIEW_AND_DECISIONS`.
No joint acceptance, independent PASS, implementation authorization, formal
main update or formal closeout is recorded by this document.

## Exact inspected inputs

| Line | Committed input | Document / meaning |
| --- | --- | --- |
| A | `3d527a970bf736142d892a4dfa4d8e39414e9161` | Shared contract + production composition design; no implementation |
| B | `f5ef62f913c8acc9e35cbe84f6ba5c9d84b3031e` | `HTTP_B_WALLET_GENERATION_DECISION.md`, document delta at 66d58fc; keeps retained-wallet exclusion |
| C | `0cbe3fdef89d1a7864429d4ed315ac65ff53e77e` | `HTTP_PRODUCTION_USER_CONTRACT_PROPOSAL.md`; acknowledges A/B boundaries and pending response decisions |

B/C were inspected read-only by exact commit, not imported, merged, edited or
re-reviewed as independent work by A. Their threads may continue after these
snapshots. The old 5329f28 wallet branch and uncommitted wallet alternatives in
the local POST worktree are reference material only, not selected models.

## Compatible baseline-preserving rules

- Durable MANUAL source and original execution/submission/receipt/effect owners
  are authoritative. HTTP is a projection and not a permission or result owner.
- Initial continuation comes from the first durable admission commit, not the
  first intake or composite duplicate flag. Every action reloads durable gates.
  No scheduler, replay, operator resume or eventual-execution promise is added.
- B's operative scope keeps the existing second-order exclusion by wallet ID
  OR normalized address, including after success/revert. Thus this iteration
  can only target a first-order production candidate, not a generally usable
  continuous same-wallet service. This is not acceptance of a new wallet model.
- Old query/recovery remains available under stop/revoke/TTL expiry when its
  own DB/RPC/lease/integrity checks pass; no signer/sender is passed to recovery.
- C07 remains unchanged; 001–045 stay immutable. Any future overlapping-risk
  model, asset-dependency contract or authorization schema is separately scoped.
- First marker ambiguity and exact-artifact/one-first-permit semantics remain.
  No automatic resend/rebuild/resign/replacement/fee bump/repair nonce.

## Mainline dispositions and outstanding decisions

These are author scope dispositions, not independent review results or public
API changes. They narrow the next candidate; none approves activation.

| Item | Current fact / proposed difference | Mainline disposition |
| --- | --- | --- |
| C 10s default / 30s maximum | Candidate end-to-end response budget, not a measured current SLA | Defer until execution-capable scope; do not copy test-only five-minute budget |
| C 202 on a readable accepted timeout | Current generic deadline error can yield 503; acceptance is not eventual execution | Preserve existing mapping for disabled A1; later explicit A/C decision and tests |
| Revoked authorization | Current loader rejects AUTHORIZATION_NOT_ARMED and writeError maps that typed rejection to 422; C proposes grouping revoked with stop/DB failure under 503 | Record as a proposed mapping change, not current behavior; retain current behavior in A1 |
| Missing/unreadable authorization | Verified missing/invalid input may be typed 422; an operational read failure remains sanitized 503 | Do not collapse absence, invalid state and failed DB read into one status without contract approval |
| Deadline vs disconnect | Once disconnected, no guarantee a Location header or response reaches the client | Retain request/key recovery where acceptance committed; do not promise wire delivery or undo committed work |
| GET has no query RPC | Applies to order/by-key/events history; other existing balance/token/fee routes can use read-only RPC | Keep this route distinction; no new query-triggered reconciliation |
| Production initial owner | Existing local harness selects from internal first-admission result; production API has no typed handoff yet | Design only; no A1 references/calls into private executor or a public execute-by-ID endpoint |
| Recovery startup independence | Existing worker graph excludes signer; current app.Run still requires execution key before recovery starts | A1 candidate isolates explicit recovery-only startup; old service behavior stays a separate profile |
| MANUAL grant | Declared no-arm guard remains; local seed is synthetic | No grant protocol/guard change in A1; production first action remains unavailable |
| Same-wallet reuse | B selects current rejection rather than a new coexistence model | Do not implement retained gross-exposure alternative or claim repeat trading solved |

The revoked/missing authorization distinctions above are source inspection,
not a new end-to-end HTTP revocation test result. Full mapping lives in
`internal/tradeapi/manual_admission_inputs.go`, `manual_verification_error.go`
and `http.go`. Preserve stop-first priority before these later checks.

## Gate and implementation dependency split

Gate 1 is not complete: the author compatibility check does not replace the
individual independent reviews or joint acceptance. B's conservative boundary
does not require new schema but also does not deliver repeat trading. C's new
timeout and error proposals remain pending.

The next proposed A1 is narrower than a complete production POST composer:
**recovery-only startup and capability separation, with MANUAL action paths
unreachable**. Its exact candidate scope is in
`HTTP_A1_DISABLED_COMPOSITION_SCOPE.md`. It deliberately does not depend on
selecting a new HTTP wait budget, a new wallet model or an operator grant.
Those decisions still block their respective later implementation scopes.
Narrowing A1 is a proposal to review/accept, not a way to skip Gate 1.

Later A/B/C integration must pin exact approved commits and preserve original
records. A one-line shared status or the presence of three design documents
cannot turn all designs/implementations into PASS. Do not cherry-pick each
branch's entire SHA256SUMS independently: preserve common entries and append
verified evidence entries explicitly when an integration batch is authorized.

## Read-only baseline checks executed in this author batch

At A code/docs head `3d527a970bf736142d892a4dfa4d8e39414e9161`, Docker Go 1.26.6,
network none, source/modules read-only, cache writable:

```sh
go test ./internal/config ./internal/app ./internal/trade ./internal/tradeapi \
  -run '^(TestLoadDefaultsFailClosed|TestLoadRejectsControlledCanaryProductionEnablementInW4A|TestUserTradeAPIStartupDefaultOffAndScopedRegistration|TestShutdownFailureStillDrainsRecovery|TestShutdownAndDrainPreservesBothErrors|TestW4ADisabledProductionCompositionFailsClosed|TestW4ARejectsAnyProductionEnablement|TestW4AProductionCompositionHasNoEconomicDependencies|TestManualSubmissionPrivateLocalDependencyBoundary|TestLocalPostExecutionNoProductionRegistrationAndNoArm)$' \
  -count=1 -timeout=3m -v
```

All 10 selected top-level tests passed: config 0.011s, app 0.369s,
trade 0.541s, tradeapi 0.381s. These are inherited boundary checks, not A1
implementation acceptance, a whole-repository regression, race validation or
independent review. Reflection/lexical tests are not arbitrary whole-program
reachability proofs. One existing no-arm fixture uses ephemeral local signing
and fake execution setup; its restored guard is checked before the target
mutation. Do not claim every fixture signer call was zero.

Production broadcast NOT_CONNECTED; authorization NOT_GRANTED; live=false;
release_ready=false. No code/schema/test/config/OpenAPI edits, B/C mutations,
push, PR, merge, real RPC transaction, grant or new implementation branch here.
