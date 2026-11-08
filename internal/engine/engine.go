// Package engine provides the top-level SQL engine API. It composes the approved modules:
// internal/storage (catalog and scans), internal/parser (SQL to AST), internal/planner
// (AST to optimized physical plan, EXPLAIN text) and internal/exec (vectorized operators).
package engine

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/exec"
	"github.com/Trysac/u409-columnar-sql-engine/internal/parser"
	"github.com/Trysac/u409-columnar-sql-engine/internal/planner"
	"github.com/Trysac/u409-columnar-sql-engine/internal/storage"
)

// Default engine option constants.
const (
	DefaultMaxScanBytes int64 = 104857600 // 100MB
	DefaultBatchSize    int   = 1024
	DefaultMaxMemory    int64 = 268435456 // 256MB
	// MaxQueryLength is the longest SQL text, in bytes, that Query and Explain accept.
	MaxQueryLength = 100000
)

// Engine implements interfaces.EngineAPI.
type Engine struct {
	mu      sync.RWMutex
	opts    interfaces.Options
	catalog *storage.Catalog
	planner *planner.Planner
	exec    *exec.ExecutionEngine
	closed  bool
}

var _ interfaces.EngineAPI = (*Engine)(nil)

// Open initializes a new Engine; zero or negative options take their defaults.
func Open(opts interfaces.Options) *Engine {
	if opts.MaxScanBytes <= 0 {
		opts.MaxScanBytes = DefaultMaxScanBytes
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.MaxMemory <= 0 {
		opts.MaxMemory = DefaultMaxMemory
	}
	return &Engine{
		opts:    opts,
		catalog: storage.NewCatalog(),
		planner: planner.NewPlanner(planner.NewOptimizer()),
		exec:    exec.NewExecutionEngine(opts.BatchSize, opts.MaxMemory),
	}
}

// New creates a new Engine with default options.
func New() *Engine {
	return Open(interfaces.Options{})
}

// Close closes the engine and releases its tables. Closing twice is a no-op.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	return e.catalog.Close()
}

// RegisterCSV registers a CSV table (header row first) with the given column types.
func (e *Engine) RegisterCSV(name string, r io.Reader, schema []interfaces.DataType) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return errClosed()
	}
	return e.catalog.RegisterCSV(name, r, schema, e.opts.MaxScanBytes, e.opts.BatchSize)
}

// Query parses, plans and executes a SELECT statement and returns a row cursor.
func (e *Engine) Query(ctx context.Context, sql string) (interfaces.RowsCursor, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stmt, plan, err := e.prepare(ctx, sql)
	if err != nil {
		return nil, err
	}
	b := e.newBuilder(stmt, false)
	op, err := b.build(ctx, plan)
	if err != nil {
		return nil, err
	}
	batches, err := drain(ctx, op, e.opts.MaxMemory)
	if err != nil {
		return nil, err
	}
	return &cursor{batches: batches, rowIdx: -1}, nil
}

// Explain returns the physical plan text. With analyze, the query runs and every operator
// line carries its observed rows, batches and duration.
func (e *Engine) Explain(ctx context.Context, sql string, analyze bool) (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stmt, plan, err := e.prepare(ctx, sql)
	if err != nil {
		return "", err
	}
	if !analyze {
		return e.planner.Explain(plan, false, nil), nil
	}
	b := e.newBuilder(stmt, true)
	op, err := b.build(ctx, plan)
	if err != nil {
		return "", err
	}
	if _, err := drain(ctx, op, e.opts.MaxMemory); err != nil {
		return "", err
	}
	return e.planner.Explain(plan, true, b.metrics()), nil
}

func (e *Engine) prepare(ctx context.Context, sql string) (*interfaces.SelectStatement, interfaces.PhysicalPlanNode, error) {
	if e.closed {
		return nil, nil, errClosed()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(sql) > MaxQueryLength {
		return nil, nil, &interfaces.EngineError{
			Code:    interfaces.ErrQueryTooLarge,
			Message: "query text exceeds maximum allowed length",
		}
	}
	stmt, err := parser.New().Parse(sql)
	if err != nil {
		return nil, nil, err
	}
	plan, err := e.planner.Plan(ctx, stmt, e.catalog)
	if err != nil {
		return nil, nil, err
	}
	if plan == nil {
		return nil, nil, &interfaces.EngineError{
			Code:    interfaces.ErrUnsupportedStatement,
			Message: "statement produced an empty plan",
		}
	}
	return stmt, plan, nil
}

func (e *Engine) newBuilder(stmt *interfaces.SelectStatement, analyze bool) *builder {
	return &builder{
		stmt:      stmt,
		catalog:   e.catalog,
		evaluator: e.exec.Evaluator(),
		batchSize: e.opts.BatchSize,
		analyze:   analyze,
		now:       time.Now,
	}
}

// drain initializes the operator pipeline and collects every non-empty batch, failing with
// limit_exceeded once the estimated result size passes maxMemory.
func drain(ctx context.Context, op interfaces.Operator, maxMemory int64) ([]*interfaces.RecordBatch, error) {
	defer op.Close()
	if err := op.Init(ctx); err != nil {
		return nil, err
	}
	var batches []*interfaces.RecordBatch
	var used int64
	for {
		batch, err := op.Next(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return batches, nil
		}
		if batch.Length == 0 {
			continue
		}
		used += batchBytes(batch)
		if used > maxMemory {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrLimitExceeded,
				Message: "query result exceeds the MaxMemory limit",
			}
		}
		batches = append(batches, batch)
	}
}

func batchBytes(batch *interfaces.RecordBatch) int64 {
	var n int64
	for _, col := range batch.Columns {
		n += int64(col.Length) * 9 // value slot plus null flag
		for _, s := range col.Strings {
			n += int64(len(s))
		}
	}
	return n
}

func errClosed() error {
	return &interfaces.EngineError{
		Code:    interfaces.ErrTableNotFound,
		Message: "engine is closed",
	}
}
