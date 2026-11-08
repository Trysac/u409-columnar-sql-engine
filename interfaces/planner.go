package interfaces

import (
	"context"
	"time"
)

// PhysicalPlanNode represents an operator node in the physical execution tree.
type PhysicalPlanNode interface {
	Name() string
	Children() []PhysicalPlanNode
	Schema() []DataType
	ColumnNames() []string
	Explain(analyze bool) string
}

// OperatorMetrics records runtime metrics for EXPLAIN ANALYZE.
type OperatorMetrics struct {
	OperatorName string
	RowsEmitted  int64
	BatchesCount int64
	Duration     time.Duration
}

// QueryPlanner transforms AST statements into optimized physical plans.
type QueryPlanner interface {
	Plan(ctx context.Context, stmt *SelectStatement, catalog TableCatalog) (PhysicalPlanNode, error)
	Explain(plan PhysicalPlanNode, analyze bool, metrics []OperatorMetrics) string
}

// RuleOptimizer applies rule-based optimizations (pushdowns, folding).
type RuleOptimizer interface {
	Optimize(plan PhysicalPlanNode) (PhysicalPlanNode, error)
}
