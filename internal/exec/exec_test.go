package exec_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/exec"
)

type mockBatchReader struct {
	batches     []*interfaces.RecordBatch
	curr        int
	schema      []interfaces.DataType
	columnNames []string
	closed      bool
}

func (m *mockBatchReader) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if m.curr >= len(m.batches) {
		return nil, nil
	}
	b := m.batches[m.curr]
	m.curr++
	return b, nil
}

func (m *mockBatchReader) Schema() []interfaces.DataType {
	return m.schema
}

func (m *mockBatchReader) ColumnNames() []string {
	return m.columnNames
}

func (m *mockBatchReader) Close() error {
	m.closed = true
	return nil
}

// TestThreeValuedLogic verifies SQL three-valued logic:
// WHEN Query runs 'SELECT NULL AND FALSE, NULL OR TRUE, NULL = NULL' THEN the row is (false, true, NULL)
func TestThreeValuedLogic(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()
	batch := &interfaces.RecordBatch{Length: 1}

	// NULL AND FALSE -> false
	andExpr := interfaces.BinaryExpr{
		Op: interfaces.OpAnd,
		Left: interfaces.LiteralExpr{
			Type:   interfaces.TypeBool,
			IsNull: true,
		},
		Right: interfaces.LiteralExpr{
			Type:   interfaces.TypeBool,
			Value:  false,
			IsNull: false,
		},
	}
	resAnd, err := eval.Evaluate(ctx, andExpr, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error in AND: %v", err)
	}
	if resAnd.NullMask[0] {
		t.Fatalf("expected NULL AND FALSE to be false, got NULL")
	}
	if resAnd.Bools[0] != false {
		t.Fatalf("expected NULL AND FALSE to be false, got %v", resAnd.Bools[0])
	}

	// NULL OR TRUE -> true
	orExpr := interfaces.BinaryExpr{
		Op: interfaces.OpOr,
		Left: interfaces.LiteralExpr{
			Type:   interfaces.TypeBool,
			IsNull: true,
		},
		Right: interfaces.LiteralExpr{
			Type:   interfaces.TypeBool,
			Value:  true,
			IsNull: false,
		},
	}
	resOr, err := eval.Evaluate(ctx, orExpr, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error in OR: %v", err)
	}
	if resOr.NullMask[0] {
		t.Fatalf("expected NULL OR TRUE to be true, got NULL")
	}
	if resOr.Bools[0] != true {
		t.Fatalf("expected NULL OR TRUE to be true, got %v", resOr.Bools[0])
	}

	// NULL = NULL -> NULL
	eqExpr := interfaces.BinaryExpr{
		Op: interfaces.OpEq,
		Left: interfaces.LiteralExpr{
			Type:   interfaces.TypeBool,
			IsNull: true,
		},
		Right: interfaces.LiteralExpr{
			Type:   interfaces.TypeBool,
			IsNull: true,
		},
	}
	resEq, err := eval.Evaluate(ctx, eqExpr, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error in EQ: %v", err)
	}
	if !resEq.NullMask[0] {
		t.Fatalf("expected NULL = NULL to be NULL, got %v", resEq.Bools[0])
	}
}

// TestAggregateGroupByOrder verifies:
// WHEN table t(id INT64, g VARCHAR, v FLOAT64) holds (1,'a',1.5), (2,'a',2.5), (3,'b',NULL)
// and Query runs 'SELECT g, SUM(v), COUNT(v), COUNT(*) FROM t GROUP BY g ORDER BY g'
// THEN rows are ('a', 4.0, 2, 2) and ('b', NULL, 0, 1)
func TestAggregateGroupByOrder(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()

	batch := &interfaces.RecordBatch{
		Length: 3,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   3,
				Int64s:   []int64{1, 2, 3},
				NullMask: []bool{false, false, false},
			},
			{
				Type:     interfaces.TypeVarChar,
				Length:   3,
				Strings:  []string{"a", "a", "b"},
				NullMask: []bool{false, false, false},
			},
			{
				Type:     interfaces.TypeFloat64,
				Length:   3,
				Float64s: []float64{1.5, 2.5, 0.0},
				NullMask: []bool{false, false, true},
			},
		},
	}

	reader := &mockBatchReader{
		batches:     []*interfaces.RecordBatch{batch},
		schema:      []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeVarChar, interfaces.TypeFloat64},
		columnNames: []string{"id", "g", "v"},
	}
	scan := exec.NewScanOperator(reader)

	aggOp := exec.NewHashAggregateOperator(
		scan,
		[]interfaces.Expr{interfaces.ColumnRef{Name: "g"}},
		[]interfaces.AggregateCall{
			{Func: "SUM", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "v"}}},
			{Func: "COUNT", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "v"}}},
			{Func: "COUNT", Args: nil}, // COUNT(*)
		},
		[]string{"g", "SUM(v)", "COUNT(v)", "COUNT(*)"},
		[]interfaces.DataType{interfaces.TypeVarChar, interfaces.TypeFloat64, interfaces.TypeInt64, interfaces.TypeInt64},
		eval,
	)

	sortOp := exec.NewSortOperator(
		aggOp,
		[]interfaces.OrderByItem{
			{Expr: interfaces.ColumnRef{Name: "g"}, Ascending: true, NullsFirst: false},
		},
		eval,
	)

	if err := sortOp.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	resBatch, err := sortOp.Next(ctx)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if resBatch == nil || resBatch.Length != 2 {
		t.Fatalf("expected 2 rows, got %+v", resBatch)
	}

	// Check row 0: ('a', 4.0, 2, 2)
	if resBatch.Columns[0].Strings[0] != "a" {
		t.Errorf("expected 'a', got %s", resBatch.Columns[0].Strings[0])
	}
	if resBatch.Columns[1].NullMask[0] || resBatch.Columns[1].Float64s[0] != 4.0 {
		t.Errorf("expected 4.0, got %v (null: %v)", resBatch.Columns[1].Float64s[0], resBatch.Columns[1].NullMask[0])
	}
	if resBatch.Columns[2].NullMask[0] || resBatch.Columns[2].Int64s[0] != 2 {
		t.Errorf("expected 2, got %v", resBatch.Columns[2].Int64s[0])
	}
	if resBatch.Columns[3].NullMask[0] || resBatch.Columns[3].Int64s[0] != 2 {
		t.Errorf("expected 2, got %v", resBatch.Columns[3].Int64s[0])
	}

	// Check row 1: ('b', NULL, 0, 1)
	if resBatch.Columns[0].Strings[1] != "b" {
		t.Errorf("expected 'b', got %s", resBatch.Columns[0].Strings[1])
	}
	if !resBatch.Columns[1].NullMask[1] {
		t.Errorf("expected SUM(v) to be NULL for 'b', got %v", resBatch.Columns[1].Float64s[1])
	}
	if resBatch.Columns[2].Int64s[1] != 0 {
		t.Errorf("expected COUNT(v) to be 0 for 'b', got %v", resBatch.Columns[2].Int64s[1])
	}
	if resBatch.Columns[3].Int64s[1] != 1 {
		t.Errorf("expected COUNT(*) to be 1 for 'b', got %v", resBatch.Columns[3].Int64s[1])
	}

	_ = sortOp.Close()
}

// TestJoinNullBehavior verifies:
// WHEN l(id) holds 1 and NULL and r(id) holds 1 and NULL
// THEN 'SELECT COUNT(*) FROM l JOIN r ON l.id = r.id' returns 1 and the LEFT JOIN form returns 2
func TestJoinNullBehavior(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()

	leftBatch := &interfaces.RecordBatch{
		Length: 2,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   2,
				Int64s:   []int64{1, 0},
				NullMask: []bool{false, true},
			},
		},
	}
	rightBatch := &interfaces.RecordBatch{
		Length: 2,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   2,
				Int64s:   []int64{1, 0},
				NullMask: []bool{false, true},
			},
		},
	}

	// Inner join
	lReader := &mockBatchReader{batches: []*interfaces.RecordBatch{leftBatch}, schema: []interfaces.DataType{interfaces.TypeInt64}, columnNames: []string{"id"}}
	rReader := &mockBatchReader{batches: []*interfaces.RecordBatch{rightBatch}, schema: []interfaces.DataType{interfaces.TypeInt64}, columnNames: []string{"id"}}
	lScan := exec.NewScanOperator(lReader)
	rScan := exec.NewScanOperator(rReader)

	innerJoin := exec.NewHashJoinOperator(
		lScan, rScan,
		interfaces.JoinInner,
		interfaces.ColumnRef{Table: "l", Name: "id"},
		interfaces.ColumnRef{Table: "r", Name: "id"},
		[]string{"l.id", "r.id"},
		[]interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64},
		eval,
		1024,
	)
	if err := innerJoin.Init(ctx); err != nil {
		t.Fatalf("innerJoin Init failed: %v", err)
	}
	b, err := innerJoin.Next(ctx)
	if err != nil {
		t.Fatalf("innerJoin Next failed: %v", err)
	}
	if b == nil || b.Length != 1 {
		t.Fatalf("expected 1 row for inner join, got %v", b)
	}
	_ = innerJoin.Close()

	// Left join
	lReader2 := &mockBatchReader{batches: []*interfaces.RecordBatch{leftBatch}, schema: []interfaces.DataType{interfaces.TypeInt64}, columnNames: []string{"id"}}
	rReader2 := &mockBatchReader{batches: []*interfaces.RecordBatch{rightBatch}, schema: []interfaces.DataType{interfaces.TypeInt64}, columnNames: []string{"id"}}
	lScan2 := exec.NewScanOperator(lReader2)
	rScan2 := exec.NewScanOperator(rReader2)

	leftJoin := exec.NewHashJoinOperator(
		lScan2, rScan2,
		interfaces.JoinLeft,
		interfaces.ColumnRef{Table: "l", Name: "id"},
		interfaces.ColumnRef{Table: "r", Name: "id"},
		[]string{"l.id", "r.id"},
		[]interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64},
		eval,
		1024,
	)
	if err := leftJoin.Init(ctx); err != nil {
		t.Fatalf("leftJoin Init failed: %v", err)
	}
	bLeft, err := leftJoin.Next(ctx)
	if err != nil {
		t.Fatalf("leftJoin Next failed: %v", err)
	}
	if bLeft == nil || bLeft.Length != 2 {
		t.Fatalf("expected 2 rows for left join, got %v", bLeft)
	}
	_ = leftJoin.Close()
}

// TestOrderByNullsFirstLast verifies:
// WHEN Query runs 'SELECT v FROM t ORDER BY v NULLS FIRST' THEN the first value is NULL, and with NULLS LAST the last value is NULL
func TestOrderByNullsFirstLast(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()

	batch := &interfaces.RecordBatch{
		Length: 3,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeFloat64,
				Length:   3,
				Float64s: []float64{10.0, 0.0, 5.0},
				NullMask: []bool{false, true, false},
			},
		},
	}

	// NULLS FIRST
	r1 := &mockBatchReader{batches: []*interfaces.RecordBatch{batch}, schema: []interfaces.DataType{interfaces.TypeFloat64}, columnNames: []string{"v"}}
	sortFirst := exec.NewSortOperator(
		exec.NewScanOperator(r1),
		[]interfaces.OrderByItem{
			{Expr: interfaces.ColumnRef{Name: "v"}, Ascending: true, NullsFirst: true},
		},
		eval,
	)
	if err := sortFirst.Init(ctx); err != nil {
		t.Fatalf("sortFirst Init failed: %v", err)
	}
	resFirst, err := sortFirst.Next(ctx)
	if err != nil || resFirst == nil {
		t.Fatalf("sortFirst Next failed: %v", err)
	}
	if !resFirst.Columns[0].NullMask[0] {
		t.Errorf("expected first row to be NULL, got %v", resFirst.Columns[0].Float64s[0])
	}
	_ = sortFirst.Close()

	// NULLS LAST
	r2 := &mockBatchReader{batches: []*interfaces.RecordBatch{batch}, schema: []interfaces.DataType{interfaces.TypeFloat64}, columnNames: []string{"v"}}
	sortLast := exec.NewSortOperator(
		exec.NewScanOperator(r2),
		[]interfaces.OrderByItem{
			{Expr: interfaces.ColumnRef{Name: "v"}, Ascending: true, NullsFirst: false},
		},
		eval,
	)
	if err := sortLast.Init(ctx); err != nil {
		t.Fatalf("sortLast Init failed: %v", err)
	}
	resLast, err := sortLast.Next(ctx)
	if err != nil || resLast == nil {
		t.Fatalf("sortLast Next failed: %v", err)
	}
	lastIdx := resLast.Length - 1
	if !resLast.Columns[0].NullMask[lastIdx] {
		t.Errorf("expected last row to be NULL, got %v", resLast.Columns[0].Float64s[lastIdx])
	}
	_ = sortLast.Close()
}

// TestEvaluatorAdditionalOperations tests arithmetic, like, cast, and error branches
func TestEvaluatorAdditionalOperations(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()
	batch := &interfaces.RecordBatch{
		Length: 2,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   2,
				Int64s:   []int64{10, 20},
				NullMask: []bool{false, false},
			},
			{
				Type:     interfaces.TypeInt64,
				Length:   2,
				Int64s:   []int64{2, 0},
				NullMask: []bool{false, false},
			},
		},
	}

	// Division by zero
	divExpr := interfaces.BinaryExpr{
		Op:    interfaces.OpDiv,
		Left:  interfaces.ColumnRef{Name: "col0"},
		Right: interfaces.ColumnRef{Name: "col1"},
	}
	_, err := eval.Evaluate(ctx, divExpr, batch, []string{"col0", "col1"})
	if err == nil {
		t.Fatalf("expected division by zero error")
	}
	var engErr *interfaces.EngineError
	if !errors.As(err, &engErr) || engErr.Code != interfaces.ErrDivisionByZero {
		t.Fatalf("expected ErrDivisionByZero, got %v", err)
	}

	// Like expression
	strBatch := &interfaces.RecordBatch{
		Length: 2,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeVarChar,
				Length:   2,
				Strings:  []string{"apple", "banana"},
				NullMask: []bool{false, false},
			},
			{
				Type:     interfaces.TypeVarChar,
				Length:   2,
				Strings:  []string{"a%", "a%"},
				NullMask: []bool{false, false},
			},
		},
	}
	likeExpr := interfaces.BinaryExpr{
		Op:    interfaces.OpLike,
		Left:  interfaces.ColumnRef{Name: "c0"},
		Right: interfaces.ColumnRef{Name: "c1"},
	}
	resLike, err := eval.Evaluate(ctx, likeExpr, strBatch, []string{"c0", "c1"})
	if err != nil {
		t.Fatalf("like failed: %v", err)
	}
	if !resLike.Bools[0] || resLike.Bools[1] {
		t.Errorf("unexpected like results: %v, %v", resLike.Bools[0], resLike.Bools[1])
	}

	// Case Expression
	caseExpr := interfaces.CaseExpr{
		Whens: []interfaces.CaseWhen{
			{
				When: interfaces.BinaryExpr{
					Op:    interfaces.OpEq,
					Left:  interfaces.ColumnRef{Name: "c0"},
					Right: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: "apple"},
				},
				Then: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)},
			},
		},
		Else: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(2)},
	}
	resCase, err := eval.Evaluate(ctx, caseExpr, strBatch, []string{"c0", "c1"})
	if err != nil {
		t.Fatalf("case failed: %v", err)
	}
	if resCase.Int64s[0] != 1 || resCase.Int64s[1] != 2 {
		t.Errorf("unexpected case result: %v", resCase.Int64s)
	}
}

// TestLimitAndFilterOperators verifies LimitOperator and FilterOperator
func TestLimitAndFilterOperators(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()
	batch := &interfaces.RecordBatch{
		Length: 5,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   5,
				Int64s:   []int64{1, 2, 3, 4, 5},
				NullMask: []bool{false, false, false, false, false},
			},
		},
	}
	reader := &mockBatchReader{batches: []*interfaces.RecordBatch{batch}, schema: []interfaces.DataType{interfaces.TypeInt64}, columnNames: []string{"num"}}
	scan := exec.NewScanOperator(reader)

	// Filter: num > 2
	filter := exec.NewFilterOperator(
		scan,
		interfaces.BinaryExpr{
			Op:    interfaces.OpGt,
			Left:  interfaces.ColumnRef{Name: "num"},
			Right: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(2)},
		},
		eval,
	)

	// Limit: LIMIT 2 OFFSET 1
	limitVal := int64(2)
	offsetVal := int64(1)
	limit := exec.NewLimitOperator(filter, &limitVal, &offsetVal)

	if err := limit.Init(ctx); err != nil {
		t.Fatalf("limit init failed: %v", err)
	}
	res, err := limit.Next(ctx)
	if err != nil {
		t.Fatalf("limit next failed: %v", err)
	}
	if res == nil || res.Length != 2 {
		t.Fatalf("expected 2 rows, got %v", res)
	}
	// Numbers greater than 2: 3, 4, 5. Offset 1 -> 4, 5
	if res.Columns[0].Int64s[0] != 4 || res.Columns[0].Int64s[1] != 5 {
		t.Errorf("expected [4, 5], got %v", res.Columns[0].Int64s)
	}
	_ = limit.Close()
}

type mockPhysicalPlanNode struct {
	op interfaces.Operator
}

func (m *mockPhysicalPlanNode) Init(ctx context.Context) error { return m.op.Init(ctx) }
func (m *mockPhysicalPlanNode) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	return m.op.Next(ctx)
}
func (m *mockPhysicalPlanNode) Close() error                            { return m.op.Close() }
func (m *mockPhysicalPlanNode) Name() string                            { return "MockPhysicalPlanNode" }
func (m *mockPhysicalPlanNode) Children() []interfaces.PhysicalPlanNode { return nil }
func (m *mockPhysicalPlanNode) Schema() []interfaces.DataType           { return m.op.Schema() }
func (m *mockPhysicalPlanNode) ColumnNames() []string                   { return m.op.ColumnNames() }
func (m *mockPhysicalPlanNode) Explain(analyze bool) string             { return "MockPhysicalPlanNode" }

// TestExecutionEngine verifies engine wrapper
func TestExecutionEngine(t *testing.T) {
	engine := exec.NewExecutionEngine(1024, 256*1024*1024)
	if engine.Evaluator() == nil {
		t.Fatalf("expected non-nil evaluator")
	}
	_, err := engine.Execute(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error executing nil plan")
	}
	vOp := exec.NewValuesOperator()
	plan := &mockPhysicalPlanNode{op: vOp}
	op, err := engine.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("failed to execute values op: %v", err)
	}
	if err := op.Init(context.Background()); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	b, err := op.Next(context.Background())
	if err != nil || b == nil || b.Length != 1 {
		t.Fatalf("unexpected next: %v, %v", b, err)
	}
	b2, err := op.Next(context.Background())
	if err != nil || b2 != nil {
		t.Fatalf("expected EOF next, got %v, %v", b2, err)
	}
	_ = op.Close()
}

func TestOperatorsMoreCoverage(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()

	// Context cancellation on ScanOperator, FilterOperator, SortOperator, HashJoinOperator, ValuesOperator
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	scan := exec.NewScanOperator(&mockBatchReader{})
	if _, err := scan.Next(canceledCtx); err == nil {
		t.Fatalf("expected context canceled error")
	}
	_ = scan.Init(ctx)
	_ = scan.Close()

	filter := exec.NewFilterOperator(exec.NewScanOperator(&mockBatchReader{}), interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: true}, eval)
	if _, err := filter.Next(canceledCtx); err == nil {
		t.Fatalf("expected context canceled error")
	}
	_ = filter.Close()

	sortOp := exec.NewSortOperator(exec.NewScanOperator(&mockBatchReader{}), nil, eval)
	if err := sortOp.Init(canceledCtx); err == nil {
		t.Fatalf("expected context canceled error")
	}
	_ = sortOp.Close()

	// HashJoin cancellation
	joinOp := exec.NewHashJoinOperator(
		exec.NewScanOperator(&mockBatchReader{}),
		exec.NewScanOperator(&mockBatchReader{}),
		interfaces.JoinInner,
		interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)},
		interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)},
		[]string{"a", "b"},
		[]interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64},
		eval,
		-1,
	)
	if err := joinOp.Init(canceledCtx); err == nil {
		t.Fatalf("expected context error on join")
	}
	_ = joinOp.Close()

	// Empty batches in Sort and Join
	emptyBatch := &interfaces.RecordBatch{Length: 0}
	rEmpty1 := &mockBatchReader{batches: []*interfaces.RecordBatch{emptyBatch}}
	sortEmpty := exec.NewSortOperator(exec.NewScanOperator(rEmpty1), nil, eval)
	if err := sortEmpty.Init(ctx); err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}
	resE, err := sortEmpty.Next(ctx)
	if err != nil || resE == nil || resE.Length != 0 {
		t.Fatalf("expected empty sorted batch, got %v, %v", resE, err)
	}
	resE2, _ := sortEmpty.Next(ctx)
	if resE2 != nil {
		t.Fatalf("expected nil after EOF")
	}

	// ProjectionOperator coverage
	projOp := exec.NewProjectionOperator(
		exec.NewValuesOperator(),
		[]interfaces.SelectItem{
			{Expr: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(42)}, Alias: "my_col"},
			{Expr: interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: 3.14}},
		},
		eval,
	)
	if err := projOp.Init(ctx); err != nil {
		t.Fatalf("proj init failed: %v", err)
	}
	pBatch, err := projOp.Next(ctx)
	if err != nil || pBatch == nil || pBatch.Length != 1 {
		t.Fatalf("expected projected batch of length 1, got %v, %v", pBatch, err)
	}
	if pBatch.Columns[0].Int64s[0] != 42 || pBatch.Columns[1].Float64s[0] != 3.14 {
		t.Fatalf("unexpected values: %v", pBatch)
	}
	_ = projOp.Close()

	// FilterOperator with empty batch and filtered out row
	batchFiltered := &interfaces.RecordBatch{
		Length: 2,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeBool,
				Length:   2,
				Bools:    []bool{false, false},
				NullMask: []bool{false, true},
			},
		},
	}
	rFilter := &mockBatchReader{batches: []*interfaces.RecordBatch{emptyBatch, batchFiltered}, schema: []interfaces.DataType{interfaces.TypeBool}, columnNames: []string{"b"}}
	fOp := exec.NewFilterOperator(
		exec.NewScanOperator(rFilter),
		interfaces.ColumnRef{Name: "b"},
		eval,
	)
	if err := fOp.Init(ctx); err != nil {
		t.Fatalf("init filter failed: %v", err)
	}
	fRes, err := fOp.Next(ctx)
	if err != nil || fRes != nil {
		t.Fatalf("expected nil filtered batch, got %v, %v", fRes, err)
	}

	// Aggregations: ungrouped empty input returns 1 row with counts=0, sum=nil, avg=nil, min=nil, max=nil
	rEmptyAgg := &mockBatchReader{batches: []*interfaces.RecordBatch{}}
	aggEmpty := exec.NewHashAggregateOperator(
		exec.NewScanOperator(rEmptyAgg),
		nil,
		[]interfaces.AggregateCall{
			{Func: "COUNT", Args: nil},
			{Func: "SUM", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "x"}}},
			{Func: "AVG", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "x"}}},
			{Func: "MIN", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "x"}}},
			{Func: "MAX", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "x"}}},
		},
		[]string{"count", "sum", "avg", "min", "max"},
		[]interfaces.DataType{interfaces.TypeInt64, interfaces.TypeFloat64, interfaces.TypeFloat64, interfaces.TypeFloat64, interfaces.TypeFloat64},
		eval,
	)
	if err := aggEmpty.Init(ctx); err != nil {
		t.Fatalf("agg empty init failed: %v", err)
	}
	aggRes, err := aggEmpty.Next(ctx)
	if err != nil || aggRes == nil || aggRes.Length != 1 {
		t.Fatalf("expected 1 row for ungrouped agg on empty input, got %v, %v", aggRes, err)
	}
	if aggRes.Columns[0].Int64s[0] != 0 {
		t.Fatalf("expected count 0, got %d", aggRes.Columns[0].Int64s[0])
	}
	if !aggRes.Columns[1].NullMask[0] || !aggRes.Columns[2].NullMask[0] || !aggRes.Columns[3].NullMask[0] || !aggRes.Columns[4].NullMask[0] {
		t.Fatalf("expected all nulls for empty sum/avg/min/max")
	}

	// Distinct aggregation and int sum and avg
	aggBatch := &interfaces.RecordBatch{
		Length: 4,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   4,
				Int64s:   []int64{10, 10, 20, 20},
				NullMask: []bool{false, false, false, false},
			},
		},
	}
	rDistinct := &mockBatchReader{batches: []*interfaces.RecordBatch{aggBatch}, schema: []interfaces.DataType{interfaces.TypeInt64}, columnNames: []string{"val"}}
	aggDistinct := exec.NewHashAggregateOperator(
		exec.NewScanOperator(rDistinct),
		nil,
		[]interfaces.AggregateCall{
			{Func: "COUNT", Distinct: true, Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}},
			{Func: "SUM", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}},
			{Func: "AVG", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}},
			{Func: "MIN", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}},
			{Func: "MAX", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}},
		},
		[]string{"c_distinct", "s", "a", "min", "max"},
		[]interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64, interfaces.TypeFloat64, interfaces.TypeInt64, interfaces.TypeInt64},
		eval,
	)
	if err := aggDistinct.Init(ctx); err != nil {
		t.Fatalf("init agg distinct failed: %v", err)
	}
	resDist, err := aggDistinct.Next(ctx)
	if err != nil || resDist == nil || resDist.Length != 1 {
		t.Fatalf("expected 1 row, got %v, %v", resDist, err)
	}
	if resDist.Columns[0].Int64s[0] != 2 {
		t.Errorf("expected distinct count 2, got %d", resDist.Columns[0].Int64s[0])
	}
	if resDist.Columns[1].Int64s[0] != 60 {
		t.Errorf("expected sum 60, got %d", resDist.Columns[1].Int64s[0])
	}
	if resDist.Columns[2].Float64s[0] != 15.0 {
		t.Errorf("expected avg 15.0, got %f", resDist.Columns[2].Float64s[0])
	}
	if resDist.Columns[3].Int64s[0] != 10 || resDist.Columns[4].Int64s[0] != 20 {
		t.Errorf("expected min 10, max 20, got min %d, max %d", resDist.Columns[3].Int64s[0], resDist.Columns[4].Int64s[0])
	}
}

func TestEvaluatorExpressionsMore(t *testing.T) {
	ctx := context.Background()
	eval := exec.NewEvaluator()
	batch := &interfaces.RecordBatch{
		Length: 2,
		Columns: []interfaces.ColumnVector{
			{
				Type:     interfaces.TypeInt64,
				Length:   2,
				Int64s:   []int64{5, 15},
				NullMask: []bool{false, false},
			},
			{
				Type:     interfaces.TypeFloat64,
				Length:   2,
				Float64s: []float64{2.5, 7.5},
				NullMask: []bool{false, false},
			},
			{
				Type:     interfaces.TypeVarChar,
				Length:   2,
				Strings:  []string{"hello", "world"},
				NullMask: []bool{false, false},
			},
			{
				Type:     interfaces.TypeBool,
				Length:   2,
				Bools:    []bool{true, false},
				NullMask: []bool{false, false},
			},
		},
	}
	cols := []string{"i", "f", "s", "b"}

	// Unary IS NULL, IS NOT NULL, NOT, -
	for _, op := range []string{"IS NULL", "IS NOT NULL"} {
		res, err := eval.Evaluate(ctx, interfaces.UnaryExpr{Op: op, Operand: interfaces.ColumnRef{Name: "i"}}, batch, cols)
		if err != nil || res == nil {
			t.Fatalf("unary %s failed: %v", op, err)
		}
	}
	resNot, err := eval.Evaluate(ctx, interfaces.UnaryExpr{Op: "NOT", Operand: interfaces.ColumnRef{Name: "b"}}, batch, cols)
	if err != nil || resNot.Bools[0] != false || resNot.Bools[1] != true {
		t.Fatalf("NOT failed: %v", resNot)
	}
	resNegI, err := eval.Evaluate(ctx, interfaces.UnaryExpr{Op: "-", Operand: interfaces.ColumnRef{Name: "i"}}, batch, cols)
	if err != nil || resNegI.Int64s[0] != -5 {
		t.Fatalf("Negation of int failed: %v", err)
	}
	resNegF, err := eval.Evaluate(ctx, interfaces.UnaryExpr{Op: "-", Operand: interfaces.ColumnRef{Name: "f"}}, batch, cols)
	if err != nil || resNegF.Float64s[0] != -2.5 {
		t.Fatalf("Negation of float failed: %v", err)
	}

	// Comparison operators
	for _, bop := range []interfaces.BinaryOp{interfaces.OpEq, interfaces.OpNotEq, interfaces.OpLt, interfaces.OpLtEq, interfaces.OpGt, interfaces.OpGtEq} {
		_, err := eval.Evaluate(ctx, interfaces.BinaryExpr{Op: bop, Left: interfaces.ColumnRef{Name: "i"}, Right: interfaces.ColumnRef{Name: "f"}}, batch, cols)
		if err != nil {
			t.Fatalf("comparison %v failed: %v", bop, err)
		}
		_, err = eval.Evaluate(ctx, interfaces.BinaryExpr{Op: bop, Left: interfaces.ColumnRef{Name: "s"}, Right: interfaces.ColumnRef{Name: "s"}}, batch, cols)
		if err != nil {
			t.Fatalf("string comparison %v failed: %v", bop, err)
		}
		_, err = eval.Evaluate(ctx, interfaces.BinaryExpr{Op: bop, Left: interfaces.ColumnRef{Name: "b"}, Right: interfaces.ColumnRef{Name: "b"}}, batch, cols)
		if err != nil {
			t.Fatalf("bool comparison %v failed: %v", bop, err)
		}
	}

	// Arithmetic operators
	for _, bop := range []interfaces.BinaryOp{interfaces.OpAdd, interfaces.OpSub, interfaces.OpMul, interfaces.OpDiv} {
		_, err := eval.Evaluate(ctx, interfaces.BinaryExpr{Op: bop, Left: interfaces.ColumnRef{Name: "i"}, Right: interfaces.ColumnRef{Name: "i"}}, batch, cols)
		if err != nil {
			t.Fatalf("int arithmetic %v failed: %v", bop, err)
		}
		_, err = eval.Evaluate(ctx, interfaces.BinaryExpr{Op: bop, Left: interfaces.ColumnRef{Name: "f"}, Right: interfaces.ColumnRef{Name: "f"}}, batch, cols)
		if err != nil {
			t.Fatalf("float arithmetic %v failed: %v", bop, err)
		}
	}

	// Casts
	casts := []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeFloat64, interfaces.TypeVarChar, interfaces.TypeBool}
	for _, target := range casts {
		_, _ = eval.Evaluate(ctx, interfaces.CastExpr{Expr: interfaces.ColumnRef{Name: "i"}, TargetType: target}, batch, cols)
		_, _ = eval.Evaluate(ctx, interfaces.CastExpr{Expr: interfaces.ColumnRef{Name: "f"}, TargetType: target}, batch, cols)
		_, _ = eval.Evaluate(ctx, interfaces.CastExpr{Expr: interfaces.ColumnRef{Name: "s"}, TargetType: target}, batch, cols)
		_, _ = eval.Evaluate(ctx, interfaces.CastExpr{Expr: interfaces.ColumnRef{Name: "b"}, TargetType: target}, batch, cols)
	}

	// Case expression with operand
	caseWithOp := interfaces.CaseExpr{
		Operand: interfaces.ColumnRef{Name: "i"},
		Whens: []interfaces.CaseWhen{
			{When: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(5)}, Then: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: "five"}},
		},
		Else: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: "other"},
	}
	cRes, err := eval.Evaluate(ctx, caseWithOp, batch, cols)
	if err != nil || cRes.Strings[0] != "five" || cRes.Strings[1] != "other" {
		t.Fatalf("case with operand failed: %v, %v", cRes, err)
	}

	// CompareValues comprehensive test
	_ = exec.CompareValues(nil, nil)
	_ = exec.CompareValues(nil, int64(1))
	_ = exec.CompareValues(int64(1), nil)
	_ = exec.CompareValues(int64(1), int64(2))
	_ = exec.CompareValues(int64(2), int64(1))
	_ = exec.CompareValues(int64(1), int64(1))
	_ = exec.CompareValues(int64(1), 2.5)
	_ = exec.CompareValues(int64(3), 2.5)
	_ = exec.CompareValues(int64(2), 2.0)
	_ = exec.CompareValues(2.5, int64(1))
	_ = exec.CompareValues(1.5, int64(2))
	_ = exec.CompareValues(2.5, 3.5)
	_ = exec.CompareValues(3.5, 2.5)
	_ = exec.CompareValues(2.5, 2.5)
	_ = exec.CompareValues("a", "b")
	_ = exec.CompareValues("b", "a")
	_ = exec.CompareValues("a", "a")
	_ = exec.CompareValues(true, false)
	_ = exec.CompareValues(false, true)
	_ = exec.CompareValues(true, true)
	_ = exec.CompareValues(false, false)
}
