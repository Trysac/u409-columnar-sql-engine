# Project record

## Objective

Embeddable columnar SQL query engine in Go with vectorized batch execution, a rule-based optimizer, CSV tables and three-valued NULL logic.

## Scope

- In scope: the public API documented in [README.md](../README.md), documented and tested.
- Out of scope: anything that needs dependencies or I/O without an accepted ADR.

## Timeline

| Date | Event |
|---|---|
| 2026 | Repository created from the factory utility template. |

## Effort metrics

Measured values only; a value that has not been measured stays "pending measurement".

| Metric | Value |
|---|---|
| Tokens (input / output) | 2537823 / 51794 |
| Hours (human) | pending measurement |
| Cost (USD) | 0.5031 |

## Factory run columnar-sql-engine-storage (measured from API usage)

| Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|
| generation | gemini-3.8-flash | yes | 7987 | 47756 | 0 | 0 | 0.0925 |
| fix-1 | gemini-3.8-flash | no | 43448 | 797 | 0 | 184856 | 0.0494 |
| fix-2 | gemini-3.8-flash | no | 87263 | 982 | 0 | 642904 | 0.1173 |
| fix-3 | gemini-3.8-flash | no | 90706 | 997 | 0 | 759428 | 0.1287 |
| fix-4 | gemini-3.8-flash | no | 83279 | 1262 | 0 | 637952 | 0.1150 |
| audit | gemini-3.1-flash-lite | no | 0 | 0 | 0 | 0 | 0.0000 |

Total: $0.5031. Prices: Anthropic list prices.

## Factory run columnar-sql-engine-parser (measured from API usage)

| Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|
| generation | gemini-3.8-flash | yes | 8108 | 19854 | 0 | 0 | 0.0403 |
| fix-1 | gemini-3.8-flash | no | 53612 | 625 | 0 | 283913 | 0.0638 |
| audit | gemini-3.1-flash-lite | no | 35828 | 147 | 0 | 18268 | 0.0096 |

Total: $0.1137. Prices: Anthropic list prices.
