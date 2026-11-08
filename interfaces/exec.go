package interfaces

import "context"

// Operator represents a physical vectorized execution operator.
type Operator interface {
	Init(ctx context.Context) error
	Next(ctx context.Context) (*RecordBatch, error)
	Schema() []DataType
	ColumnNames() []string
	Close() error
}

// ExecutionContext provides resource tracking and cancellation.
type ExecutionContext interface {
	Context() context.Context
	BatchSize() int
	MaxMemory() int64
}

// VectorEvaluator evaluates expressions over RecordBatches.
type VectorEvaluator interface {
	Evaluate(ctx context.Context, expr Expr, batch *RecordBatch, columnNames []string) (*ColumnVector, error)
}

// ExecutionEngine creates executable operator pipelines from physical plans.
type ExecutionEngine interface {
	Execute(ctx context.Context, plan PhysicalPlanNode) (Operator, error)
	Evaluator() VectorEvaluator
}
