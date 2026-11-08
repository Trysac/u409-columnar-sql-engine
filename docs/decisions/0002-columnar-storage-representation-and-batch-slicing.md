# Columnar storage representation and batch slicing

Status: accepted (generated, pending human review)

## Context

The SQL engine requires columnar storage for CSV-backed tables, with strong memory-bound guarantees during ingestion (MaxScanBytes limit of 100MB by default), support for typed column vectors (Int64, Float64, Decimal, VarChar, Date, Bool) with null bitmasks, and vectorized batch scanning emitting 1024-row record batches.

## Decision

Implement TableCatalog with in-memory columnar storage where each table column retains typed slices and a boolean null bitmask. Apply a byte-counting reader during CSV parsing that strictly enforces MaxScanBytes and aborts with ErrLimitExceeded. Slicing in BatchReader emits batches of up to batchSize rows (default 1024) with projected columns and zero external dependencies.

## Consequences

In-memory scans iterate sequentially with high cache locality and predictable batch sizes. Ingestion memory limits prevent unbounded memory growth from untrusted CSV streams.
