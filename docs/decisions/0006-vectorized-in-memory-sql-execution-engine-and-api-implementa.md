# Vectorized In-Memory SQL Execution Engine and API Implementation

Status: accepted (generated, pending human review)

## Context

The columnar SQL engine requires a top-level engine interface (EngineAPI) supporting table registration from CSV streams, query execution yielding row cursors (RowsCursor), and EXPLAIN query plan inspection, as well as a comprehensive golden queries test suite.

## Decision

Implement the complete EngineAPI and RowsCursor in internal/engine. Query planning parses SELECT queries, validates column references, coordinates joins, filters, hash aggregates, and sorts according to three-valued NULL logic, and emits column vectors in record batches conforming to configured batch sizes.

## Consequences

Provides full compliance with EngineAPI and frozen interface specifications while adhering strictly to zero external runtime dependency rules and executing golden queries deterministically.
