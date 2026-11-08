# Rule-Based Physical Query Planning and EXPLAIN Formatting

Status: accepted (generated, pending human review)

## Context

The columnar SQL engine requires transforming parsed SQL SELECT AST statements into physical operator trees, applying optimizations (predicate pushdown, projection pushdown, constant folding, and cardinality-based join reordering), and rendering human-readable EXPLAIN / EXPLAIN ANALYZE plan hierarchies with operator row counts and durations.

## Decision

We implemented `Planner` adhering to `interfaces.QueryPlanner` and `DefaultOptimizer` implementing `interfaces.RuleOptimizer`. Physical plan nodes (`ScanNode`, `FilterNode`, `ProjectNode`, `HashAggregateNode`, `HashJoinNode`, `SortNode`, `LimitNode`) implement `interfaces.PhysicalPlanNode`. Rule optimizations fold scalar constants across AST expressions (respecting three-valued boolean logic), push predicates to scan sources, eliminate redundant projection columns, and arrange join relations by row count. `Explain` renders indented operator trees and formats runtime `OperatorMetrics` when `analyze` is set.

## Consequences

Query plans are vectorized-ready physical operator trees with minimal column scans and folded constants. EXPLAIN plans provide transparent visibility into plan structure and execution metrics.
