# C02｜Chain / Contract Baseline

Status: **C02-PONS-CURVE PARTIAL / C02-PONS-V4 PARTIAL / C02-LONG NOT STARTED**.
The combined capture remains `CAPTURED_REQUIRES_CLASSIFICATION_REVIEW`; no path is yet C02 PASS.

The read-only capture at block `64376127` (`0x7607f685fe685f7952bd366fb338ee32c26bd35f517a11065a725ebb9ebaefad`)
confirmed chain ID 4663 and non-empty runtime code for every Pons V0.1 and shared Uniswap v4
address selected from the locked Trade SDK. Exact addresses, Keccak-256 runtime hashes and code
sizes are in `evidence/chain-baseline.json` and the production-consumable copy is
`configs/mainnet/contracts.lock.json` in the repository root.

The capture also reads the ERC-1967 implementation slot at the same block. WETH and USDG had
non-zero implementation addresses with non-empty implementation code. Other entries are labeled
`no_eip1967_implementation_slot`; this does **not** prove that every one is non-proxy, because
beacon, diamond, minimal-clone and custom proxy patterns require separate classification.

## Reproduction

Use a credential-free or secret-injected read-only RPC endpoint:

```bash
ROBINHOOD_RPC_URL=... go run ./cmd/chain-baseline \
  -block 64376127 \
  -output configs/mainnet/contracts.lock.json
```

The command permits only chain/header, code and storage reads through `ethclient`; it has no key,
signing or send path. The endpoint itself is redacted from evidence.

The generator fails closed if the locked Parser and Trade SDK shared AddressBooks disagree.
`TestLockedSDKAddressBooksMatch` runs the same comparison in CI without requiring RPC access;
the baseline contract list also has an offline uniqueness/non-zero-address test.

## Remaining C02 gates

- Independently classify every `no_eip1967_implementation_slot` entry.
- Verify beacon/custom proxy slots where applicable.
- Record authoritative deployment/source evidence, not only an RPC observation.
- Re-run the locked-block capture from an independent archive RPC.

Until these gates are reviewed, `status` remains `CAPTURED_REQUIRES_CLASSIFICATION_REVIEW` and
execution must fail closed.
