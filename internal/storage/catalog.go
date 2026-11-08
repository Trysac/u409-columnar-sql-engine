package storage

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

const (
	// DefaultMaxScanBytes is the default maximum scan size (100MB).
	DefaultMaxScanBytes int64 = 104857600
	// DefaultBatchSize is the default batch row count (1024).
	DefaultBatchSize int = 1024
	// MaxCatalogTables is the maximum number of tables that can be registered.
	MaxCatalogTables = 10000
	// MaxTableNameLength is the maximum length of a table name.
	MaxTableNameLength = 1024
)

type storedColumn struct {
	name     string
	dataType interfaces.DataType
	int64s   []int64
	float64s []float64
	strings  []string
	bools    []bool
	nullMask []bool
}

type storedTable struct {
	name        string
	columnNames []string
	schema      []interfaces.DataType
	columns     []storedColumn
	rowCount    int64
	byteSize    int64
	batchSize   int
}

// Catalog manages registered CSV tables and creates batch scanners.
type Catalog struct {
	mu     sync.RWMutex
	tables map[string]*storedTable
	closed bool
}

// NewCatalog creates a new in-memory TableCatalog.
func NewCatalog() *Catalog {
	return &Catalog{
		tables: make(map[string]*storedTable),
	}
}

var _ interfaces.TableCatalog = (*Catalog)(nil)

type countingReader struct {
	r         io.Reader
	bytesRead int64
	maxBytes  int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.bytesRead += int64(n)
	if cr.maxBytes > 0 && cr.bytesRead > cr.maxBytes {
		return 0, &interfaces.EngineError{
			Code:    interfaces.ErrLimitExceeded,
			Message: fmt.Sprintf("CSV source exceeded max scan bytes limit of %d bytes", cr.maxBytes),
		}
	}
	return n, err
}

// RegisterCSV registers a table parsed from CSV data into typed column vectors.
func (c *Catalog) RegisterCSV(name string, r io.Reader, schema []interfaces.DataType, maxScanBytes int64, batchSize int) error {
	if name == "" {
		return &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: "table name cannot be empty",
		}
	}
	if len(name) > MaxTableNameLength {
		return &interfaces.EngineError{
			Code:    interfaces.ErrLimitExceeded,
			Message: fmt.Sprintf("table name exceeds maximum length of %d", MaxTableNameLength),
		}
	}
	if r == nil {
		return &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: "reader cannot be nil",
		}
	}
	if len(schema) == 0 {
		return &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: "schema cannot be empty",
		}
	}

	if maxScanBytes <= 0 {
		maxScanBytes = DefaultMaxScanBytes
	}
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return &interfaces.EngineError{
			Code:    interfaces.ErrLimitExceeded,
			Message: "catalog is closed",
		}
	}

	if _, exists := c.tables[name]; !exists && len(c.tables) >= MaxCatalogTables {
		return &interfaces.EngineError{
			Code:    interfaces.ErrLimitExceeded,
			Message: "catalog table limit exceeded",
		}
	}

	cr := &countingReader{
		r:        r,
		maxBytes: maxScanBytes,
	}

	csvReader := csv.NewReader(cr)
	csvReader.LazyQuotes = true
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err != nil {
		var engErr *interfaces.EngineError
		if errors.As(err, &engErr) {
			return engErr
		}
		if err == io.EOF {
			return &interfaces.EngineError{
				Code:    interfaces.ErrSyntaxError,
				Message: "empty CSV source",
			}
		}
		return &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("failed to read CSV header: %s", err.Error()),
		}
	}

	if len(header) != len(schema) {
		return &interfaces.EngineError{
			Code: interfaces.ErrTypeMismatch,
			Message: fmt.Sprintf("schema column count (%d) does not match CSV header column count (%d)",
				len(schema), len(header)),
		}
	}

	colNames := make([]string, len(header))
	seenNames := make(map[string]struct{}, len(header))
	for i, h := range header {
		clean := strings.TrimSpace(h)
		if clean == "" {
			return &interfaces.EngineError{
				Code:    interfaces.ErrSyntaxError,
				Message: fmt.Sprintf("empty column name at index %d in CSV header", i),
			}
		}
		lower := strings.ToLower(clean)
		if _, seen := seenNames[lower]; seen {
			return &interfaces.EngineError{
				Code:    interfaces.ErrSyntaxError,
				Message: fmt.Sprintf("duplicate column name %q in CSV header", clean),
			}
		}
		seenNames[lower] = struct{}{}
		colNames[i] = clean
	}

	columns := make([]storedColumn, len(schema))
	for i := range schema {
		columns[i] = storedColumn{
			name:     colNames[i],
			dataType: schema[i],
		}
	}

	rowCount := int64(0)
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var engErr *interfaces.EngineError
			if errors.As(err, &engErr) {
				return engErr
			}
			return &interfaces.EngineError{
				Code:    interfaces.ErrSyntaxError,
				Message: fmt.Sprintf("failed to read CSV row: %s", err.Error()),
			}
		}

		if len(record) != len(schema) {
			return &interfaces.EngineError{
				Code: interfaces.ErrSyntaxError,
				Message: fmt.Sprintf("row %d has %d fields, expected %d",
					rowCount+1, len(record), len(schema)),
			}
		}

		for i, fieldVal := range record {
			col := &columns[i]
			dt := schema[i]
			if isNullValue(fieldVal, dt) {
				col.nullMask = append(col.nullMask, true)
				switch dt {
				case interfaces.TypeInt64:
					col.int64s = append(col.int64s, 0)
				case interfaces.TypeFloat64:
					col.float64s = append(col.float64s, 0)
				case interfaces.TypeDecimal:
					col.float64s = append(col.float64s, 0)
					col.strings = append(col.strings, "")
				case interfaces.TypeVarChar:
					col.strings = append(col.strings, "")
				case interfaces.TypeDate:
					col.strings = append(col.strings, "")
					col.int64s = append(col.int64s, 0)
				case interfaces.TypeBool:
					col.bools = append(col.bools, false)
				}
			} else {
				i64, f64, str, b, parseErr := parseFieldValue(fieldVal, colNames[i], dt)
				if parseErr != nil {
					return parseErr
				}
				col.nullMask = append(col.nullMask, false)
				switch dt {
				case interfaces.TypeInt64:
					col.int64s = append(col.int64s, i64)
				case interfaces.TypeFloat64:
					col.float64s = append(col.float64s, f64)
				case interfaces.TypeDecimal:
					col.float64s = append(col.float64s, f64)
					col.strings = append(col.strings, str)
				case interfaces.TypeVarChar:
					col.strings = append(col.strings, str)
				case interfaces.TypeDate:
					col.strings = append(col.strings, str)
					col.int64s = append(col.int64s, i64)
				case interfaces.TypeBool:
					col.bools = append(col.bools, b)
				}
			}
		}
		rowCount++
	}

	schemaCopy := make([]interfaces.DataType, len(schema))
	copy(schemaCopy, schema)

	table := &storedTable{
		name:        name,
		columnNames: colNames,
		schema:      schemaCopy,
		columns:     columns,
		rowCount:    rowCount,
		byteSize:    cr.bytesRead,
		batchSize:   batchSize,
	}

	c.tables[name] = table
	return nil
}

func (c *Catalog) findTable(name string) *storedTable {
	if t, ok := c.tables[name]; ok {
		return t
	}
	for k, t := range c.tables {
		if strings.EqualFold(k, name) {
			return t
		}
	}
	return nil
}

// GetTable returns table metadata by name.
func (c *Catalog) GetTable(name string) (*interfaces.TableMetadata, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrLimitExceeded,
			Message: "catalog is closed",
		}
	}

	table := c.findTable(name)
	if table == nil {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrTableNotFound,
			Message: fmt.Sprintf("table %q not found", truncateForError(name)),
		}
	}

	cols := make([]string, len(table.columnNames))
	copy(cols, table.columnNames)
	sch := make([]interfaces.DataType, len(table.schema))
	copy(sch, table.schema)

	return &interfaces.TableMetadata{
		Name:        table.name,
		ColumnNames: cols,
		Schema:      sch,
		RowCount:    table.rowCount,
		ByteSize:    table.byteSize,
	}, nil
}

// ListTables returns sorted table names registered in the catalog.
func (c *Catalog) ListTables() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed || len(c.tables) == 0 {
		return []string{}
	}

	names := make([]string, 0, len(c.tables))
	for name := range c.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Close closes the catalog and releases registered resources.
func (c *Catalog) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.tables = make(map[string]*storedTable)
	return nil
}

// ScanTable creates a batch reader for scanning table rows.
func (c *Catalog) ScanTable(ctx context.Context, name string, projections []string, batchSize int) (interfaces.BatchReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrLimitExceeded,
			Message: "catalog is closed",
		}
	}

	table := c.findTable(name)
	if table == nil {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrTableNotFound,
			Message: fmt.Sprintf("table %q not found", truncateForError(name)),
		}
	}

	if batchSize <= 0 {
		batchSize = table.batchSize
	}
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	colIndices := make([]int, 0, len(table.columns))
	if len(projections) == 0 {
		for i := range table.columns {
			colIndices = append(colIndices, i)
		}
	} else {
		for _, proj := range projections {
			idx := -1
			for i, cn := range table.columnNames {
				if strings.EqualFold(cn, proj) {
					idx = i
					break
				}
			}
			if idx == -1 {
				return nil, &interfaces.EngineError{
					Code: interfaces.ErrColumnNotFound,
					Message: fmt.Sprintf("column %q not found in table %q",
						truncateForError(proj), truncateForError(name)),
				}
			}
			colIndices = append(colIndices, idx)
		}
	}

	projSchema := make([]interfaces.DataType, len(colIndices))
	projNames := make([]string, len(colIndices))
	for i, idx := range colIndices {
		projSchema[i] = table.schema[idx]
		projNames[i] = table.columnNames[idx]
	}

	return newBatchReader(table, colIndices, projSchema, projNames, batchSize), nil
}
