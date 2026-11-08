package storage

import (
	"context"
	"sync"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

type batchReader struct {
	mu         sync.Mutex
	table      *storedTable
	colIndices []int
	schema     []interfaces.DataType
	names      []string
	batchSize  int
	cursor     int
	closed     bool
}

func newBatchReader(
	table *storedTable,
	colIndices []int,
	schema []interfaces.DataType,
	names []string,
	batchSize int,
) *batchReader {
	return &batchReader{
		table:      table,
		colIndices: colIndices,
		schema:     schema,
		names:      names,
		batchSize:  batchSize,
		cursor:     0,
	}
}

var _ interfaces.BatchReader = (*batchReader)(nil)

// Schema returns the projected schema DataTypes.
func (r *batchReader) Schema() []interfaces.DataType {
	sch := make([]interfaces.DataType, len(r.schema))
	copy(sch, r.schema)
	return sch
}

// ColumnNames returns the projected column names.
func (r *batchReader) ColumnNames() []string {
	names := make([]string, len(r.names))
	copy(names, r.names)
	return names
}

// Close closes the batch reader.
func (r *batchReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// Next fetches the next RecordBatch.
func (r *batchReader) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, nil
	}

	totalRows := int(r.table.rowCount)
	if r.cursor >= totalRows {
		return nil, nil
	}

	remaining := totalRows - r.cursor
	n := r.batchSize
	if n > remaining {
		n = remaining
	}

	start := r.cursor
	end := start + n
	r.cursor = end

	columns := make([]interfaces.ColumnVector, len(r.colIndices))
	for i, colIdx := range r.colIndices {
		sc := &r.table.columns[colIdx]
		cv := interfaces.ColumnVector{
			Type:     sc.dataType,
			Length:   n,
			NullMask: make([]bool, n),
		}
		copy(cv.NullMask, sc.nullMask[start:end])

		switch sc.dataType {
		case interfaces.TypeInt64:
			cv.Int64s = make([]int64, n)
			copy(cv.Int64s, sc.int64s[start:end])
		case interfaces.TypeFloat64:
			cv.Float64s = make([]float64, n)
			copy(cv.Float64s, sc.float64s[start:end])
		case interfaces.TypeDecimal:
			cv.Float64s = make([]float64, n)
			copy(cv.Float64s, sc.float64s[start:end])
			cv.Strings = make([]string, n)
			copy(cv.Strings, sc.strings[start:end])
		case interfaces.TypeVarChar:
			cv.Strings = make([]string, n)
			copy(cv.Strings, sc.strings[start:end])
		case interfaces.TypeDate:
			cv.Strings = make([]string, n)
			copy(cv.Strings, sc.strings[start:end])
			if len(sc.int64s) >= end {
				cv.Int64s = make([]int64, n)
				copy(cv.Int64s, sc.int64s[start:end])
			}
		case interfaces.TypeBool:
			cv.Bools = make([]bool, n)
			copy(cv.Bools, sc.bools[start:end])
		}
		columns[i] = cv
	}

	return &interfaces.RecordBatch{
		Columns: columns,
		Length:  n,
	}, nil
}
