package interfaces

import (
	"context"
	"io"
)

// DataType identifies the columnar physical type.
type DataType uint8

const (
	TypeInt64 DataType = iota
	TypeFloat64
	TypeDecimal
	TypeVarChar
	TypeDate
	TypeBool
)

// ColumnVector represents a single column chunk with null bitmask.
type ColumnVector struct {
	Type     DataType
	Int64s   []int64
	Float64s []float64
	Strings  []string
	Bools    []bool
	NullMask []bool // true indicates NULL
	Length   int
}

// RecordBatch groups column vectors of the same length.
type RecordBatch struct {
	Columns []ColumnVector
	Length  int
}

// EngineError represents a domain error with a stable machine-readable code.
type EngineError struct {
	Code    string
	Message string
}

func (e *EngineError) Error() string { return e.Message }

const (
	ErrSyntaxError          = "syntax_error"
	ErrUnsupportedStatement = "unsupported_statement"
	ErrTypeMismatch         = "type_mismatch"
	ErrTableNotFound        = "table_not_found"
	ErrColumnNotFound       = "column_not_found"
	ErrLimitExceeded        = "limit_exceeded"
	ErrDivisionByZero       = "division_by_zero"
	ErrQueryTooLarge        = "query_too_large"
	ErrDepthExceeded        = "depth_exceeded"
)

// TableMetadata contains schema and statistics for a registered table.
type TableMetadata struct {
	Name        string
	ColumnNames []string
	Schema      []DataType
	RowCount    int64
	ByteSize    int64
}

// BatchReader iterates over record batches.
type BatchReader interface {
	Next(ctx context.Context) (*RecordBatch, error)
	Schema() []DataType
	ColumnNames() []string
	Close() error
}

// TableCatalog manages registered CSV tables and creates batch scanners.
type TableCatalog interface {
	RegisterCSV(name string, r io.Reader, schema []DataType, maxScanBytes int64, batchSize int) error
	GetTable(name string) (*TableMetadata, error)
	ScanTable(ctx context.Context, name string, projections []string, batchSize int) (BatchReader, error)
	ListTables() []string
	Close() error
}
