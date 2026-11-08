package engine

import (
	"fmt"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// cursor iterates the result batches of a query row by row.
type cursor struct {
	batches  []*interfaces.RecordBatch
	batchIdx int
	rowIdx   int
	closed   bool
}

var _ interfaces.RowsCursor = (*cursor)(nil)

// Next advances to the next row and reports whether one exists.
func (c *cursor) Next() bool {
	if c.closed {
		return false
	}
	for c.batchIdx < len(c.batches) {
		c.rowIdx++
		if c.rowIdx < c.batches[c.batchIdx].Length {
			return true
		}
		c.batchIdx++
		c.rowIdx = -1
	}
	return false
}

// CurrentBatch returns the batch holding the current row (the first batch before Next).
func (c *cursor) CurrentBatch() *interfaces.RecordBatch {
	if c.closed || c.batchIdx >= len(c.batches) {
		return nil
	}
	return c.batches[c.batchIdx]
}

// Close releases the result; later calls to Next return false.
func (c *cursor) Close() error {
	c.closed = true
	c.batches = nil
	return nil
}

// Scan copies the current row into dest. Each destination is *any (receives int64, float64,
// string, bool or nil), a typed pointer (*int64, *int, *float64, *string, *bool; NULL gives the
// zero value) or a pointer to pointer (**int64, **float64, **string, **bool; NULL gives nil).
func (c *cursor) Scan(dest ...any) error {
	if c.closed {
		return &interfaces.EngineError{Code: interfaces.ErrSyntaxError, Message: "cursor is closed"}
	}
	if c.batchIdx >= len(c.batches) || c.rowIdx < 0 {
		return &interfaces.EngineError{Code: interfaces.ErrSyntaxError, Message: "no current row to scan"}
	}
	batch := c.batches[c.batchIdx]
	if len(dest) != len(batch.Columns) {
		return &interfaces.EngineError{
			Code:    interfaces.ErrTypeMismatch,
			Message: fmt.Sprintf("Scan expected %d destination args, got %d", len(batch.Columns), len(dest)),
		}
	}
	for i, target := range dest {
		if err := scanValue(&batch.Columns[i], c.rowIdx, target); err != nil {
			return err
		}
	}
	return nil
}

func cellValue(col *interfaces.ColumnVector, row int) any {
	if col.NullMask[row] {
		return nil
	}
	switch col.Type {
	case interfaces.TypeInt64:
		return col.Int64s[row]
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		return col.Float64s[row]
	case interfaces.TypeBool:
		return col.Bools[row]
	default:
		return col.Strings[row]
	}
}

func mismatch(target any, col *interfaces.ColumnVector) error {
	return &interfaces.EngineError{
		Code:    interfaces.ErrTypeMismatch,
		Message: fmt.Sprintf("cannot scan column of type %d into %T", col.Type, target),
	}
}

func scanValue(col *interfaces.ColumnVector, row int, target any) error {
	v := cellValue(col, row)
	switch ptr := target.(type) {
	case *any:
		*ptr = v
	case *int64:
		return assign(ptr, v, col, target)
	case *int:
		var n int64
		if err := assign(&n, v, col, target); err != nil {
			return err
		}
		*ptr = int(n)
	case *float64:
		return assign(ptr, v, col, target)
	case *string:
		return assign(ptr, v, col, target)
	case *bool:
		return assign(ptr, v, col, target)
	case **int64:
		return assignPtr(ptr, v, col, target)
	case **float64:
		return assignPtr(ptr, v, col, target)
	case **string:
		return assignPtr(ptr, v, col, target)
	case **bool:
		return assignPtr(ptr, v, col, target)
	default:
		return &interfaces.EngineError{
			Code:    interfaces.ErrTypeMismatch,
			Message: fmt.Sprintf("unsupported scan destination type %T", target),
		}
	}
	return nil
}

func assign[T any](ptr *T, v any, col *interfaces.ColumnVector, target any) error {
	if v == nil {
		var zero T
		*ptr = zero
		return nil
	}
	typed, ok := v.(T)
	if !ok {
		return mismatch(target, col)
	}
	*ptr = typed
	return nil
}

func assignPtr[T any](ptr **T, v any, col *interfaces.ColumnVector, target any) error {
	if v == nil {
		*ptr = nil
		return nil
	}
	typed, ok := v.(T)
	if !ok {
		return mismatch(target, col)
	}
	*ptr = &typed
	return nil
}
