package exec

import (
	"context"
	"fmt"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// ExecutionEngine implements interfaces.ExecutionEngine.
type ExecutionEngine struct {
	evaluator interfaces.VectorEvaluator
	batchSize int
	maxMemory int64
}

// NewExecutionEngine creates a new execution engine.
func NewExecutionEngine(batchSize int, maxMemory int64) *ExecutionEngine {
	if batchSize <= 0 {
		batchSize = 1024
	}
	if maxMemory <= 0 {
		maxMemory = 256 * 1024 * 1024
	}
	return &ExecutionEngine{
		evaluator: NewEvaluator(),
		batchSize: batchSize,
		maxMemory: maxMemory,
	}
}

// Evaluator returns the VectorEvaluator used by this engine.
func (e *ExecutionEngine) Evaluator() interfaces.VectorEvaluator {
	return e.evaluator
}

// Execute creates an operator pipeline from a physical plan node.
func (e *ExecutionEngine) Execute(ctx context.Context, plan interfaces.PhysicalPlanNode) (interfaces.Operator, error) {
	if plan == nil {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: "nil physical plan",
		}
	}
	// If plan is already an Operator, return it.
	if op, ok := plan.(interfaces.Operator); ok {
		return op, nil
	}
	return nil, &interfaces.EngineError{
		Code:    interfaces.ErrSyntaxError,
		Message: fmt.Sprintf("cannot execute physical plan node %T", plan),
	}
}
