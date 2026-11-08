package storage_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/storage"
)

type repeatReader struct {
	chunk     []byte
	remaining int64
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	toRead := len(p)
	if int64(toRead) > r.remaining {
		toRead = int(r.remaining)
	}
	if toRead > len(r.chunk) {
		toRead = len(r.chunk)
	}
	copy(p, r.chunk[:toRead])
	r.remaining -= int64(toRead)
	return toRead, nil
}

func TestRegisterCSV_LimitExceededVerbatim(t *testing.T) {
	// Acceptance test: IF a CSV source exceeds MaxScanBytes of 104857600 bytes THEN RegisterCSV returns *EngineError with code 'limit_exceeded'
	cat := storage.NewCatalog()
	defer func() {
		if err := cat.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	header := "id,val\n"
	targetBytes := int64(104857601)
	chunk := []byte(strings.Repeat("1,test\n", 10000))
	r := io.MultiReader(
		strings.NewReader(header),
		&repeatReader{
			chunk:     chunk,
			remaining: targetBytes - int64(len(header)),
		},
	)

	schema := []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeVarChar}
	err := cat.RegisterCSV("big_table", r, schema, 104857600, 1024)
	if err == nil {
		t.Fatalf("expected RegisterCSV to fail when exceeding MaxScanBytes of 104857600")
	}

	var engineErr *interfaces.EngineError
	if !errors.As(err, &engineErr) {
		t.Fatalf("expected *EngineError, got %T: %v", err, err)
	}
	if engineErr.Code != "limit_exceeded" {
		t.Fatalf("expected code 'limit_exceeded', got %q", engineErr.Code)
	}
}

func TestScanTable_2500RowsEmitsBatchesVerbatim(t *testing.T) {
	// Acceptance test: WHEN a table of 2500 rows is scanned THEN the scan emits batches of 1024, 1024 and 452 rows
	cat := storage.NewCatalog()
	defer func() {
		if err := cat.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	var sb strings.Builder
	sb.WriteString("id,name\n")
	for i := 1; i <= 2500; i++ {
		sb.WriteString(fmt.Sprintf("%d,user%d\n", i, i))
	}

	schema := []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeVarChar}
	if err := cat.RegisterCSV("t2500", strings.NewReader(sb.String()), schema, 0, 1024); err != nil {
		t.Fatalf("RegisterCSV failed: %v", err)
	}

	scanner, err := cat.ScanTable(context.Background(), "t2500", nil, 1024)
	if err != nil {
		t.Fatalf("ScanTable failed: %v", err)
	}
	defer func() {
		if err := scanner.Close(); err != nil {
			t.Errorf("scanner.Close() error = %v", err)
		}
	}()

	ctx := context.Background()
	b1, err := scanner.Next(ctx)
	if err != nil || b1 == nil {
		t.Fatalf("batch 1 failed: %v", err)
	}
	if b1.Length != 1024 {
		t.Fatalf("batch 1 expected 1024 rows, got %d", b1.Length)
	}
	if b1.Columns[0].Int64s[0] != 1 || b1.Columns[0].Int64s[1023] != 1024 {
		t.Errorf("batch 1 content unexpected: first=%d, last=%d", b1.Columns[0].Int64s[0], b1.Columns[0].Int64s[1023])
	}

	b2, err := scanner.Next(ctx)
	if err != nil || b2 == nil {
		t.Fatalf("batch 2 failed: %v", err)
	}
	if b2.Length != 1024 {
		t.Fatalf("batch 2 expected 1024 rows, got %d", b2.Length)
	}
	if b2.Columns[0].Int64s[0] != 1025 || b2.Columns[0].Int64s[1023] != 2048 {
		t.Errorf("batch 2 content unexpected: first=%d, last=%d", b2.Columns[0].Int64s[0], b2.Columns[0].Int64s[1023])
	}

	b3, err := scanner.Next(ctx)
	if err != nil || b3 == nil {
		t.Fatalf("batch 3 failed: %v", err)
	}
	if b3.Length != 452 {
		t.Fatalf("batch 3 expected 452 rows, got %d", b3.Length)
	}
	if b3.Columns[0].Int64s[0] != 2049 || b3.Columns[0].Int64s[451] != 2500 {
		t.Errorf("batch 3 content unexpected: first=%d, last=%d", b3.Columns[0].Int64s[0], b3.Columns[0].Int64s[451])
	}

	b4, err := scanner.Next(ctx)
	if err != nil {
		t.Fatalf("batch 4 error: %v", err)
	}
	if b4 != nil {
		t.Fatalf("expected nil batch after 2500 rows, got length %d", b4.Length)
	}
}

func TestDataTypesAndNulls(t *testing.T) {
	cat := storage.NewCatalog()
	defer func() {
		_ = cat.Close()
	}()

	csvData := `id,flt,dec,str,dt,flag
1,1.5,12.34,alpha,2024-01-15,true
2,2.5,56.78,"",2024-02-20 12:00:00,1
3,NULL,\N,NULL,NULL,NULL
4,,,-,2024/03/25,no
`
	schema := []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeFloat64,
		interfaces.TypeDecimal,
		interfaces.TypeVarChar,
		interfaces.TypeDate,
		interfaces.TypeBool,
	}

	if err := cat.RegisterCSV("all_types", strings.NewReader(csvData), schema, 0, 2); err != nil {
		t.Fatalf("RegisterCSV failed: %v", err)
	}

	meta, err := cat.GetTable("all_types")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if meta.RowCount != 4 {
		t.Fatalf("expected 4 rows, got %d", meta.RowCount)
	}
	if meta.ByteSize <= 0 {
		t.Fatalf("expected positive byte size, got %d", meta.ByteSize)
	}

	r, err := cat.ScanTable(context.Background(), "all_types", nil, 2)
	if err != nil {
		t.Fatalf("ScanTable failed: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()

	if len(r.Schema()) != 6 || len(r.ColumnNames()) != 6 {
		t.Fatalf("mismatched schema/names count")
	}

	ctx := context.Background()
	b1, err := r.Next(ctx)
	if err != nil || b1.Length != 2 {
		t.Fatalf("batch 1 error: %v, len=%v", err, b1.Length)
	}

	// Check Row 1
	if b1.Columns[0].Int64s[0] != 1 || b1.Columns[0].NullMask[0] {
		t.Errorf("row 1 id unexpected")
	}
	if b1.Columns[1].Float64s[0] != 1.5 || b1.Columns[1].NullMask[0] {
		t.Errorf("row 1 flt unexpected")
	}
	if b1.Columns[2].Float64s[0] != 12.34 || b1.Columns[2].Strings[0] != "12.34" {
		t.Errorf("row 1 dec unexpected")
	}
	if b1.Columns[3].Strings[0] != "alpha" || b1.Columns[3].NullMask[0] {
		t.Errorf("row 1 str unexpected")
	}
	if b1.Columns[4].Strings[0] != "2024-01-15" || b1.Columns[4].Int64s[0] == 0 {
		t.Errorf("row 1 date unexpected")
	}
	if !b1.Columns[5].Bools[0] || b1.Columns[5].NullMask[0] {
		t.Errorf("row 1 flag unexpected")
	}

	// Check Row 2 (encoding/csv cannot tell "" from an empty field, so both are NULL)
	if !b1.Columns[3].NullMask[1] {
		t.Errorf("row 2 empty varchar should be NULL")
	}
	if !b1.Columns[5].Bools[1] {
		t.Errorf("row 2 bool 1 should be true")
	}

	b2, err := r.Next(ctx)
	if err != nil || b2.Length != 2 {
		t.Fatalf("batch 2 error: %v, len=%v", err, b2.Length)
	}

	// Check Row 3 (id 3, every other column NULL)
	if b2.Columns[0].Int64s[0] != 3 || b2.Columns[0].NullMask[0] {
		t.Errorf("row 3 id should be 3")
	}
	for c := 1; c < 6; c++ {
		if !b2.Columns[c].NullMask[0] {
			t.Errorf("column %d row 3 should be NULL", c)
		}
	}

	// Check Row 4 (id 4; empty flt and dec are NULL; bool 'no' is false)
	if b2.Columns[0].Int64s[1] != 4 || b2.Columns[0].NullMask[1] {
		t.Errorf("row 4 id should be 4")
	}
	if !b2.Columns[1].NullMask[1] {
		t.Errorf("row 4 flt should be NULL")
	}
	if !b2.Columns[2].NullMask[1] {
		t.Errorf("row 4 dec should be NULL")
	}
	if b2.Columns[5].Bools[1] != false || b2.Columns[5].NullMask[1] {
		t.Errorf("row 4 bool should be false")
	}
}

func TestProjections(t *testing.T) {
	cat := storage.NewCatalog()
	defer func() {
		_ = cat.Close()
	}()

	csvData := "a,b,c\n1,2,3\n4,5,6\n"
	schema := []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64, interfaces.TypeInt64}
	if err := cat.RegisterCSV("proj_tab", strings.NewReader(csvData), schema, 0, 10); err != nil {
		t.Fatalf("RegisterCSV failed: %v", err)
	}

	r, err := cat.ScanTable(context.Background(), "proj_tab", []string{"c", "A"}, 10)
	if err != nil {
		t.Fatalf("ScanTable failed: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()

	cols := r.ColumnNames()
	if len(cols) != 2 || cols[0] != "c" || cols[1] != "a" {
		t.Fatalf("unexpected projected column names: %v", cols)
	}

	batch, err := r.Next(context.Background())
	if err != nil || batch.Length != 2 {
		t.Fatalf("Next() error = %v, len = %v", err, batch.Length)
	}
	if batch.Columns[0].Int64s[0] != 3 || batch.Columns[1].Int64s[0] != 1 {
		t.Errorf("projection content mismatch: c=%d, a=%d", batch.Columns[0].Int64s[0], batch.Columns[1].Int64s[0])
	}

	// Non-existent projected column
	_, err = cat.ScanTable(context.Background(), "proj_tab", []string{"unknown_col"}, 10)
	if err == nil {
		t.Fatalf("expected ErrColumnNotFound for missing column")
	}
	var engineErr *interfaces.EngineError
	if !errors.As(err, &engineErr) || engineErr.Code != interfaces.ErrColumnNotFound {
		t.Fatalf("expected ErrColumnNotFound, got %v", err)
	}
}

func TestCatalogLifecycleAndErrors(t *testing.T) {
	cat := storage.NewCatalog()

	// Empty table name
	err := cat.RegisterCSV("", strings.NewReader("id\n1\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Table name too long
	longName := strings.Repeat("a", 2000)
	err = cat.RegisterCSV(longName, strings.NewReader("id\n1\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrLimitExceeded)

	// Nil reader
	err = cat.RegisterCSV("t", nil, []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Empty schema
	err = cat.RegisterCSV("t", strings.NewReader("id\n1\n"), nil, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Empty CSV source
	err = cat.RegisterCSV("t", strings.NewReader(""), []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Header column count mismatch with schema
	err = cat.RegisterCSV("t", strings.NewReader("a,b\n1,2\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	// Empty column name in header
	err = cat.RegisterCSV("t", strings.NewReader("a, \n1,2\n"), []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Duplicate column name in header
	err = cat.RegisterCSV("t", strings.NewReader("col,COL\n1,2\n"), []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Row field count mismatch
	err = cat.RegisterCSV("t", strings.NewReader("a,b\n1,2,3\n"), []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrSyntaxError)

	// Type mismatches
	err = cat.RegisterCSV("t", strings.NewReader("a\nnot_an_int\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	err = cat.RegisterCSV("t", strings.NewReader("a\nnot_a_float\n"), []interfaces.DataType{interfaces.TypeFloat64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	err = cat.RegisterCSV("t", strings.NewReader("a\nnot_a_decimal\n"), []interfaces.DataType{interfaces.TypeDecimal}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	err = cat.RegisterCSV("t", strings.NewReader("a\nnot_a_date\n"), []interfaces.DataType{interfaces.TypeDate}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	err = cat.RegisterCSV("t", strings.NewReader("a\nmaybe\n"), []interfaces.DataType{interfaces.TypeBool}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	// Unsupported data type
	err = cat.RegisterCSV("t", strings.NewReader("a\nval\n"), []interfaces.DataType{interfaces.DataType(99)}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrTypeMismatch)

	// Successful registration and ListTables
	if err := cat.RegisterCSV("users", strings.NewReader("id\n1\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10); err != nil {
		t.Fatalf("RegisterCSV failed: %v", err)
	}
	if err := cat.RegisterCSV("accounts", strings.NewReader("id\n2\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10); err != nil {
		t.Fatalf("RegisterCSV failed: %v", err)
	}
	tables := cat.ListTables()
	if len(tables) != 2 || tables[0] != "accounts" || tables[1] != "users" {
		t.Errorf("ListTables mismatch: %v", tables)
	}

	// TableNotFound
	_, err = cat.GetTable("non_existent")
	checkErrorCode(t, err, interfaces.ErrTableNotFound)
	_, err = cat.ScanTable(context.Background(), "non_existent", nil, 10)
	checkErrorCode(t, err, interfaces.ErrTableNotFound)

	// Canceled context in ScanTable
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cat.ScanTable(ctxCancel, "users", nil, 10)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Reader cancellation and close
	r, err := cat.ScanTable(context.Background(), "users", nil, 10)
	if err != nil {
		t.Fatalf("ScanTable failed: %v", err)
	}
	_, err = r.Next(ctxCancel)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in Next, got %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("r.Close() error = %v", err)
	}
	bAfterClose, err := r.Next(context.Background())
	if err != nil || bAfterClose != nil {
		t.Errorf("expected nil batch after reader close, got %v, %v", bAfterClose, err)
	}

	// Catalog close and subsequent operations
	if err := cat.Close(); err != nil {
		t.Fatalf("cat.Close() error = %v", err)
	}
	if len(cat.ListTables()) != 0 {
		t.Errorf("expected empty list after catalog close")
	}
	err = cat.RegisterCSV("foo", strings.NewReader("id\n1\n"), []interfaces.DataType{interfaces.TypeInt64}, 0, 10)
	checkErrorCode(t, err, interfaces.ErrLimitExceeded)
	_, err = cat.GetTable("users")
	checkErrorCode(t, err, interfaces.ErrLimitExceeded)
	_, err = cat.ScanTable(context.Background(), "users", nil, 10)
	checkErrorCode(t, err, interfaces.ErrLimitExceeded)
}

func checkErrorCode(t *testing.T, err error, expectedCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", expectedCode)
	}
	var engineErr *interfaces.EngineError
	if !errors.As(err, &engineErr) {
		t.Fatalf("expected *interfaces.EngineError, got %T: %v", err, err)
	}
	if engineErr.Code != expectedCode {
		t.Fatalf("expected code %q, got %q (message: %s)", expectedCode, engineErr.Code, engineErr.Message)
	}
	if engineErr.Error() == "" {
		t.Errorf("Error() should not be empty")
	}
}
