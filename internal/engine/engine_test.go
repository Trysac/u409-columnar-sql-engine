package engine_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/engine"
)

type queryTestCase struct {
	Name     string  `json:"name"`
	SQL      string  `json:"sql"`
	Expected [][]any `json:"expected"`
}

func setupTestEngine(t *testing.T) *engine.Engine {
	e := engine.New()

	// Table t
	csvT := "id,name,val\n" +
		"1,alpha,10.5\n" +
		"2,beta,20.0\n" +
		"3,gamma,null\n" +
		"4,delta,40.25\n" +
		"5,null,50.0\n"
	err := e.RegisterCSV("t", strings.NewReader(csvT), []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeVarChar,
		interfaces.TypeFloat64,
	})
	if err != nil {
		t.Fatalf("failed to register table t: %v", err)
	}

	// Table sales
	csvSales := "id,grp,amount\n" +
		"1,east,100.0\n" +
		"2,east,150.0\n" +
		"3,west,150.0\n" +
		"4,north,100.0\n"
	err = e.RegisterCSV("sales", strings.NewReader(csvSales), []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeVarChar,
		interfaces.TypeFloat64,
	})
	if err != nil {
		t.Fatalf("failed to register table sales: %v", err)
	}

	// Tables l and r
	csvL := "id,name\n" +
		"1,alice\n" +
		"2,bob\n" +
		"3,charlie\n" +
		"null,david\n"
	err = e.RegisterCSV("l", strings.NewReader(csvL), []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeVarChar,
	})
	if err != nil {
		t.Fatalf("failed to register table l: %v", err)
	}

	csvR := "id,score\n" +
		"1,95.0\n" +
		"2,80.0\n" +
		"4,70.0\n" +
		"null,85.0\n"
	err = e.RegisterCSV("r", strings.NewReader(csvR), []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeFloat64,
	})
	if err != nil {
		t.Fatalf("failed to register table r: %v", err)
	}

	// Table t_nulls matching acceptance test
	csvNulls := "id,g,v\n" +
		"1,a,1.5\n" +
		"2,a,2.5\n" +
		"3,b,null\n"
	err = e.RegisterCSV("t_nulls", strings.NewReader(csvNulls), []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeVarChar,
		interfaces.TypeFloat64,
	})
	if err != nil {
		t.Fatalf("failed to register table t_nulls: %v", err)
	}

	return e
}

func TestGoldenQueries(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()

	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "queries.json"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join("testdata", "queries.json"))
		if err != nil {
			t.Fatalf("failed to read testdata/queries.json: %v", err)
		}
	}

	var testCases []queryTestCase
	if err := json.Unmarshal(data, &testCases); err != nil {
		t.Fatalf("failed to unmarshal testdata/queries.json: %v", err)
	}

	if len(testCases) < 30 {
		t.Fatalf("expected at least 30 golden query cases, found %d", len(testCases))
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			cursor, err := eng.Query(ctx, tc.SQL)
			if err != nil {
				t.Fatalf("query '%s' failed: %v", tc.SQL, err)
			}
			defer cursor.Close()

			var actualRows [][]any
			for cursor.Next() {
				numCols := len(tc.Expected[0])
				if numCols == 0 && len(tc.Expected) > 0 {
					numCols = len(tc.Expected[0])
				}
				dests := make([]any, numCols)
				scanDests := make([]any, numCols)
				for i := range dests {
					scanDests[i] = &dests[i]
				}
				if err := cursor.Scan(scanDests...); err != nil {
					t.Fatalf("scan failed: %v", err)
				}
				actualRows = append(actualRows, dests)
			}

			if len(actualRows) != len(tc.Expected) {
				t.Fatalf("expected %d rows, got %d rows for query '%s'", len(tc.Expected), len(actualRows), tc.SQL)
			}

			for rIdx, expRow := range tc.Expected {
				actRow := actualRows[rIdx]
				if len(actRow) != len(expRow) {
					t.Fatalf("row %d: expected %d cols, got %d", rIdx, len(expRow), len(actRow))
				}
				for cIdx, expVal := range expRow {
					actVal := actRow[cIdx]
					if !valuesMatch(expVal, actVal) {
						t.Errorf("query '%s' row %d col %d: expected %v (%T), got %v (%T)",
							tc.SQL, rIdx, cIdx, expVal, expVal, actVal, actVal)
					}
				}
			}
		})
	}
}

func valuesMatch(exp, act any) bool {
	if exp == nil {
		return act == nil
	}
	if act == nil {
		return false
	}

	switch e := exp.(type) {
	case float64:
		switch a := act.(type) {
		case float64:
			return math.Abs(e-a) < 1e-4
		case int64:
			return math.Abs(e-float64(a)) < 1e-4
		}
	case int64:
		switch a := act.(type) {
		case int64:
			return e == a
		case float64:
			return math.Abs(float64(e)-a) < 1e-4
		}
	case string:
		if a, ok := act.(string); ok {
			return e == a
		}
	case bool:
		if a, ok := act.(bool); ok {
			return e == a
		}
	}
	return false
}

func TestAcceptanceCases(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()
	ctx := context.Background()

	// Acceptance 1: Acceptance item for syntax_error on 'SELECT FROM WHERE'
	_, err := eng.Query(ctx, "SELECT FROM WHERE")
	if err == nil {
		t.Fatal("expected error for 'SELECT FROM WHERE', got nil")
	}
	if engErr, ok := err.(*interfaces.EngineError); !ok || engErr.Code != interfaces.ErrSyntaxError {
		t.Fatalf("expected *EngineError with code 'syntax_error', got %v", err)
	}

	// Acceptance 2: EXPLAIN on COUNT(*) contains Scan rows=3 and HashAggregate rows=1
	explainText, err := eng.Explain(ctx, "SELECT COUNT(*) FROM t_nulls", true)
	if err != nil {
		t.Fatalf("explain failed: %v", err)
	}
	if !strings.Contains(explainText, "Scan") || !strings.Contains(explainText, "rows=3") {
		t.Errorf("explain expected 'Scan' with 'rows=3', got: %s", explainText)
	}
	if !strings.Contains(explainText, "HashAggregate") || !strings.Contains(explainText, "rows=1") {
		t.Errorf("explain expected 'HashAggregate' with 'rows=1', got: %s", explainText)
	}

	// Acceptance 3: MaxScanBytes exceeded returns limit_exceeded
	engSmall := engine.Open(interfaces.Options{MaxScanBytes: 10, BatchSize: 10})
	err = engSmall.RegisterCSV("big", strings.NewReader("id,name\n1,very_long_string_exceeding_limit\n"), []interfaces.DataType{
		interfaces.TypeInt64,
		interfaces.TypeVarChar,
	})
	if err == nil {
		t.Fatal("expected limit_exceeded error, got nil")
	}
	if engErr, ok := err.(*interfaces.EngineError); !ok || engErr.Code != interfaces.ErrLimitExceeded {
		t.Fatalf("expected *EngineError with code 'limit_exceeded', got %v", err)
	}

	// Acceptance 4: Scan batches of 2500 rows with batch size 1024 emits 1024, 1024, 452
	engBatch := engine.Open(interfaces.Options{BatchSize: 1024, MaxScanBytes: 104857600})
	var csvBuilder strings.Builder
	csvBuilder.WriteString("id\n")
	for i := 0; i < 2500; i++ {
		csvBuilder.WriteString("1\n")
	}
	err = engBatch.RegisterCSV("large_table", strings.NewReader(csvBuilder.String()), []interfaces.DataType{interfaces.TypeInt64})
	if err != nil {
		t.Fatalf("failed to register large_table: %v", err)
	}
	cursor, err := engBatch.Query(ctx, "SELECT id FROM large_table")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer cursor.Close()
	// Record each distinct batch once, by identity: two consecutive batches can have the same
	// length (1024, 1024), so comparing lengths would merge them.
	var batchSizes []int
	var last *interfaces.RecordBatch
	for cursor.Next() {
		if b := cursor.CurrentBatch(); b != last {
			batchSizes = append(batchSizes, b.Length)
			last = b
		}
	}
	if len(batchSizes) != 3 || batchSizes[0] != 1024 || batchSizes[1] != 1024 || batchSizes[2] != 452 {
		t.Errorf("expected batches of 1024, 1024, 452, got %v", batchSizes)
	}
}

func TestCursorScanTypes(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()
	ctx := context.Background()

	cursor, err := eng.Query(ctx, "SELECT id, name, val FROM t WHERE id = 1")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer cursor.Close()

	if !cursor.Next() {
		t.Fatal("expected next row")
	}

	var id int64
	var name string
	var val float64
	err = cursor.Scan(&id, &name, &val)
	if err != nil {
		t.Fatalf("typed scan failed: %v", err)
	}
	if id != 1 || name != "alpha" || val != 10.5 {
		t.Errorf("unexpected scanned values: %d, %s, %f", id, name, val)
	}
}
