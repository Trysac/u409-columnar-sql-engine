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

## Factory run columnar-sql-engine-vector-exec (measured from API usage)

| Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|
| generation | gemini-3.8-flash | yes | 8348 | 32599 | 0 | 0 | 0.0643 |
| fix-1 | gemini-3.8-flash | no | 104575 | 920 | 0 | 1094166 | 0.1639 |
| fix-2 | gemini-3.8-flash | no | 133658 | 923 | 0 | 1089837 | 0.1854 |
| fix-3 | gemini-3.8-flash | no | 128032 | 122 | 0 | 3020 | 0.0967 |
| fix-1 | gemini-3.8-flash | no | 104196 | 1382 | 0 | 1122574 | 0.1675 |
| fix-2 | gemini-3.8-flash | no | 34467 | 98 | 0 | 30249 | 0.0285 |
| fix-3 | gemini-3.8-flash | no | 101659 | 1246 | 0 | 1081490 | 0.1620 |
| fix-4 | gemini-3.8-flash | no | 108112 | 10915 | 0 | 1191661 | 0.2114 |
| fix-5 | gemini-3.8-flash | no | 61448 | 475 | 0 | 373007 | 0.0758 |
| audit | gemini-3.1-flash-lite | no | 56402 | 144 | 0 | 75549 | 0.0162 |

Total: $1.1718. Prices: Anthropic list prices.

## Factory run columnar-sql-engine-planner (measured from API usage)

| Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|
| generation | gemini-3.8-flash | yes | 8586 | 20568 | 0 | 0 | 0.0418 |
| fix-1 | gemini-3.8-flash | no | 80164 | 4360 | 0 | 684678 | 0.1278 |
| audit | gemini-3.1-flash-lite | no | 55633 | 510 | 0 | 99137 | 0.0172 |
| fix-2 | gemini-3.8-flash | no | 81081 | 3380 | 0 | 709215 | 0.1267 |

Total: $0.3134. Prices: Anthropic list prices.

## Factory run columnar-sql-engine-engine-api (measured from API usage)

| Phase | Model | Batch | Input | Output | Cache write | Cache read | Cost (USD) |
|---|---|---|---|---|---|---|---|
| generation | gemini-3.8-flash | yes | 9408 | 36961 | 0 | 0 | 0.0728 |
| fix-1 | gemini-3.8-flash | no | 33828 | 212 | 0 | 29493 | 0.0284 |
| fix-2 | gemini-3.8-flash | no | 101922 | 1017 | 0 | 1277721 | 0.1761 |
| fix-3 | gemini-3.8-flash | no | 171604 | 1066 | 0 | 1254232 | 0.2268 |
| fix-4 | gemini-3.8-flash | no | 102444 | 1041 | 0 | 1326758 | 0.1802 |
| fix-5 | gemini-3.8-flash | no | 102335 | 1036 | 0 | 1320853 | 0.1797 |
| fix-6 | gemini-3.8-flash | no | 105746 | 1059 | 0 | 1414693 | 0.1894 |
| audit | gemini-3.1-flash-lite | no | 40848 | 420 | 0 | 20045 | 0.0113 |
| fix-1 | gemini-3.8-flash | no | 91955 | 763 | 0 | 883759 | 0.1381 |

Total: $1.2028. Prices: Anthropic list prices.
