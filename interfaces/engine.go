package interfaces

import (
	"context"
	"io"
)

// Options configure the engine limits and defaults.
type Options struct {
	MaxScanBytes int64 // default: 104857600 (100MB)
	BatchSize    int   // default: 1024
	MaxMemory    int64 // default: 268435456 (256MB)
}

// RowsCursor provides iteration over query results.
type RowsCursor interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	CurrentBatch() *RecordBatch
}

// EngineAPI defines the full public SQL engine interface.
type EngineAPI interface {
	Close() error
	RegisterCSV(name string, r io.Reader, schema []DataType) error
	Query(ctx context.Context, sql string) (RowsCursor, error)
	Explain(ctx context.Context, sql string, analyze bool) (string, error)
}
