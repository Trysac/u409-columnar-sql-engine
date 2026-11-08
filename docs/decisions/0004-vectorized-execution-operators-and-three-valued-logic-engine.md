# Vectorized Execution Operators and Three-Valued Logic Engine

Status: accepted (generated, pending human review)

## Context

The columnar SQL engine requires high-performance vectorized physical operators, SQL standard three-valued boolean logic, hash-based grouping and equijoins, and deterministic sorting with NULLS FIRST / NULLS LAST support.

## Decision

Implement VectorEvaluator and core physical operators (Scan, Values, Projection, Filter, HashAggregate, HashJoin, Sort, Limit) operating directly on typed ColumnVector and RecordBatch slices, preserving null bitmasks and ANSI SQL three-valued logic rules.

## Consequences

Executes SQL query plans in vectorized batches without per-row object allocations, correctly handles equijoins and grouping with nulls, and fulfills all acceptance criteria.
