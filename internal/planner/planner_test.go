package planner_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/planner"
)

type mockCatalog struct {
	tables map[string]*interfaces.TableMetadata
}

func newMockCatalog() *mockCatalog {
	return &mockCatalog{tables: make(map[string]*interfaces.TableMetadata)}
}

func (m *mockCatalog) RegisterCSV(name string, r io.Reader, schema []interfaces.DataType, maxScanBytes int64, batchSize int) error {
	return nil
}

func (m *mockCatalog) GetTable(name string) (*interfaces.TableMetadata, error) {
	if t, ok := m.tables[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("table not found: %s", name)
}

func (m *mockCatalog) ScanTable(ctx context.Context, name string, projections []string, batchSize int) (interfaces.BatchReader, error) {
	return nil, nil
}

func (m *mockCatalog) ListTables() []string {
	var res []string
	for k := range m.tables {
		res = append(res, k)
	}
	return res
}

func (m *mockCatalog) Close() error {
	return nil
}

func TestPlannerAcceptance(t *testing.T) {
	// Acceptance test:
	// WHEN Explain(ctx, 'SELECT COUNT(*) FROM t', true) runs on the 3-row table THEN the text contains 'Scan' with 'rows=3' and 'HashAggregate' with 'rows=1'
	cat := newMockCatalog()
	cat.tables["t"] = &interfaces.TableMetadata{
		Name:        "t",
		ColumnNames: []string{"id", "v"},
		Schema:      []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeFloat64},
		RowCount:    3,
	}

	p := planner.NewPlanner(planner.NewOptimizer())
	stmt := &interfaces.SelectStatement{
		FromTable: "t",
		Projections: []interfaces.SelectItem{
			{
				Expr: interfaces.AggregateCall{
					Func: "COUNT",
					Args: []interfaces.Expr{interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: "*"}},
				},
			},
		},
	}

	ctx := context.Background()
	plan, err := p.Plan(ctx, stmt, cat)
	if err != nil {
		t.Fatalf("unexpected Plan error: %v", err)
	}

	metrics := []interfaces.OperatorMetrics{
		{
			OperatorName: "Scan",
			RowsEmitted:  3,
			BatchesCount: 1,
			Duration:     10 * time.Millisecond,
		},
		{
			OperatorName: "HashAggregate",
			RowsEmitted:  1,
			BatchesCount: 1,
			Duration:     5 * time.Millisecond,
		},
	}

	explainText := p.Explain(plan, true, metrics)
	if !strings.Contains(explainText, "Scan") || !strings.Contains(explainText, "rows=3") {
		t.Errorf("expected explainText to contain 'Scan' with 'rows=3', got:\n%s", explainText)
	}
	if !strings.Contains(explainText, "HashAggregate") || !strings.Contains(explainText, "rows=1") {
		t.Errorf("expected explainText to contain 'HashAggregate' with 'rows=1', got:\n%s", explainText)
	}

	// Also verify with empty metrics that default fallback works
	explainFallback := p.Explain(plan, true, nil)
	if !strings.Contains(explainFallback, "Scan") || !strings.Contains(explainFallback, "rows=3") {
		t.Errorf("expected fallback explainText to contain 'Scan' with 'rows=3', got:\n%s", explainFallback)
	}
	if !strings.Contains(explainFallback, "HashAggregate") || !strings.Contains(explainFallback, "rows=1") {
		t.Errorf("expected fallback explainText to contain 'HashAggregate' with 'rows=1', got:\n%s", explainFallback)
	}
}

func TestPlannerErrors(t *testing.T) {
	cat := newMockCatalog()
	p := planner.NewPlanner(nil)
	ctx := context.Background()

	// Nil statement
	_, err := p.Plan(ctx, nil, cat)
	if err == nil {
		t.Fatalf("expected error for nil statement")
	}

	// Table not found
	stmt := &interfaces.SelectStatement{FromTable: "nonexistent"}
	_, err = p.Plan(ctx, stmt, cat)
	if err == nil {
		t.Fatalf("expected error for nonexistent table")
	}
	if eErr, ok := err.(*interfaces.EngineError); !ok || eErr.Code != interfaces.ErrTableNotFound {
		t.Errorf("expected table_not_found error, got %v", err)
	}

	// Join table not found
	cat.tables["t1"] = &interfaces.TableMetadata{Name: "t1", ColumnNames: []string{"a"}, Schema: []interfaces.DataType{interfaces.TypeInt64}}
	stmtJoin := &interfaces.SelectStatement{
		FromTable: "t1",
		Joins: []interfaces.JoinClause{
			{Table: "t2"},
		},
	}
	_, err = p.Plan(ctx, stmtJoin, cat)
	if err == nil {
		t.Fatalf("expected error for nonexistent join table")
	}
}

func TestPlannerJoinOrderingAndPushdowns(t *testing.T) {
	cat := newMockCatalog()
	cat.tables["large"] = &interfaces.TableMetadata{
		Name:        "large",
		ColumnNames: []string{"id", "v1"},
		Schema:      []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeFloat64},
		RowCount:    10000,
	}
	cat.tables["small"] = &interfaces.TableMetadata{
		Name:        "small",
		ColumnNames: []string{"id", "v2"},
		Schema:      []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeFloat64},
		RowCount:    50,
	}

	lim := int64(10)
	off := int64(5)
	stmt := &interfaces.SelectStatement{
		FromTable: "large",
		Joins: []interfaces.JoinClause{
			{
				Type:      interfaces.JoinInner,
				Table:     "small",
				Condition: interfaces.BinaryExpr{Op: interfaces.OpEq, Left: interfaces.ColumnRef{Table: "large", Name: "id"}, Right: interfaces.ColumnRef{Table: "small", Name: "id"}},
			},
		},
		Where: interfaces.BinaryExpr{
			Op:    interfaces.OpAnd,
			Left:  interfaces.BinaryExpr{Op: interfaces.OpGt, Left: interfaces.ColumnRef{Table: "large", Name: "v1"}, Right: interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: 10.0}},
			Right: interfaces.BinaryExpr{Op: interfaces.OpEq, Left: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)}, Right: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)}},
		},
		Projections: []interfaces.SelectItem{
			{Expr: interfaces.ColumnRef{Table: "large", Name: "id"}, Alias: "large_id"},
			{Expr: interfaces.ColumnRef{Table: "small", Name: "v2"}},
		},
		OrderBy: []interfaces.OrderByItem{
			{Expr: interfaces.ColumnRef{Table: "large", Name: "id"}, Ascending: false, NullsFirst: true},
		},
		Limit:  &lim,
		Offset: &off,
	}

	p := planner.NewPlanner(planner.NewOptimizer())
	plan, err := p.Plan(context.Background(), stmt, cat)
	if err != nil {
		t.Fatalf("unexpected Plan error: %v", err)
	}

	explain := p.Explain(plan, false, nil)
	if !strings.Contains(explain, "Limit") || !strings.Contains(explain, "Sort") || !strings.Contains(explain, "HashJoin") {
		t.Errorf("unexpected explain output:\n%s", explain)
	}
}

func TestOptimizerConstantFolding(t *testing.T) {
	opt := planner.NewOptimizer()

	// Plan with Filter having 1 + 2 = 3 (evaluates to true)
	scan := &planner.ScanNode{
		TableName: "t",
		RowCount:  10,
	}
	filter := &planner.FilterNode{
		Child: scan,
		Condition: interfaces.BinaryExpr{
			Op: interfaces.OpEq,
			Left: interfaces.BinaryExpr{
				Op:    interfaces.OpAdd,
				Left:  interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)},
				Right: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(2)},
			},
			Right: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(3)},
		},
	}

	optimized, err := opt.Optimize(filter)
	if err != nil {
		t.Fatalf("unexpected optimize error: %v", err)
	}

	// Filter with true condition is eliminated!
	if optimized.Name() != "Scan" {
		t.Errorf("expected ScanNode after removing tautological filter, got %s", optimized.Name())
	}

	// Test nil plan
	nilPlan, err := opt.Optimize(nil)
	if err != nil || nilPlan != nil {
		t.Errorf("expected nil for nil plan")
	}
}

func TestExplainNil(t *testing.T) {
	p := planner.NewPlanner(nil)
	res := p.Explain(nil, false, nil)
	if res != "<nil>" {
		t.Errorf("expected <nil>, got %s", res)
	}
}

func TestAggregationsWithGroupByAndHaving(t *testing.T) {
	cat := newMockCatalog()
	cat.tables["t"] = &interfaces.TableMetadata{
		Name:        "t",
		ColumnNames: []string{"cat", "val"},
		Schema:      []interfaces.DataType{interfaces.TypeVarChar, interfaces.TypeFloat64},
		RowCount:    100,
	}

	stmt := &interfaces.SelectStatement{
		FromTable: "t",
		GroupBy: []interfaces.Expr{
			interfaces.ColumnRef{Name: "cat"},
		},
		Projections: []interfaces.SelectItem{
			{Expr: interfaces.AggregateCall{Func: "SUM", Distinct: true, Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}}, Alias: "sum_val"},
			{Expr: interfaces.AggregateCall{Func: "AVG", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}}},
			{Expr: interfaces.AggregateCall{Func: "MIN", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}}},
			{Expr: interfaces.AggregateCall{Func: "MAX", Args: []interfaces.Expr{interfaces.ColumnRef{Name: "val"}}}},
		},
		Having: interfaces.BinaryExpr{
			Op:    interfaces.OpGt,
			Left:  interfaces.ColumnRef{Name: "sum_val"},
			Right: interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: 50.0},
		},
	}

	p := planner.NewPlanner(nil)
	plan, err := p.Plan(context.Background(), stmt, cat)
	if err != nil {
		t.Fatalf("unexpected Plan error: %v", err)
	}

	explain := p.Explain(plan, true, nil)
	if !strings.Contains(explain, "HashAggregate") || !strings.Contains(explain, "Filter") {
		t.Errorf("expected HashAggregate and Filter in explain, got:\n%s", explain)
	}
}

func TestUnaryAndLogicalFolding(t *testing.T) {
	opt := planner.NewOptimizer()

	// NOT true -> false
	scan := &planner.ScanNode{
		TableName: "t",
		Filter: interfaces.UnaryExpr{
			Op:      "NOT",
			Operand: interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: true},
		},
	}
	optScan, err := opt.Optimize(scan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := optScan.(*planner.ScanNode)
	if !ok {
		t.Fatalf("expected ScanNode")
	}
	lit, ok := s.Filter.(interfaces.LiteralExpr)
	if !ok || lit.Value != false {
		t.Errorf("expected folded false, got %#v", s.Filter)
	}

	// IS NULL on literal
	scan.Filter = interfaces.UnaryExpr{Op: "IS NULL", Operand: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: nil, IsNull: true}}
	optScan, _ = opt.Optimize(scan)
	s = optScan.(*planner.ScanNode)
	lit = s.Filter.(interfaces.LiteralExpr)
	if lit.Value != true {
		t.Errorf("expected true, got %v", lit.Value)
	}

	// IS NOT NULL on literal
	scan.Filter = interfaces.UnaryExpr{Op: "IS NOT NULL", Operand: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: nil, IsNull: true}}
	optScan, _ = opt.Optimize(scan)
	s = optScan.(*planner.ScanNode)
	lit = s.Filter.(interfaces.LiteralExpr)
	if lit.Value != false {
		t.Errorf("expected false, got %v", lit.Value)
	}

	// AND / OR three-valued logic
	scan.Filter = interfaces.BinaryExpr{
		Op:    interfaces.OpAnd,
		Left:  interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: nil, IsNull: true},
		Right: interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: false, IsNull: false},
	}
	optScan, _ = opt.Optimize(scan)
	s = optScan.(*planner.ScanNode)
	lit = s.Filter.(interfaces.LiteralExpr)
	if lit.Value != false || lit.IsNull {
		t.Errorf("expected false, got %#v", lit)
	}

	scan.Filter = interfaces.BinaryExpr{
		Op:    interfaces.OpOr,
		Left:  interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: nil, IsNull: true},
		Right: interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: true, IsNull: false},
	}
	optScan, _ = opt.Optimize(scan)
	s = optScan.(*planner.ScanNode)
	lit = s.Filter.(interfaces.LiteralExpr)
	if lit.Value != true || lit.IsNull {
		t.Errorf("expected true, got %#v", lit)
	}
}

func TestPlannerAdditionalCoverage(t *testing.T) {
	// Exercise remaining nodes: ProjectNode, SortNode, LimitNode, HashJoinNode
	proj := &planner.ProjectNode{Child: nil}
	if proj.Name() != "Project" || len(proj.Children()) != 0 {
		t.Errorf("unexpected ProjectNode Name or Children")
	}

	sort := &planner.SortNode{Child: nil}
	if sort.Name() != "Sort" || len(sort.Children()) != 0 {
		t.Errorf("unexpected SortNode Name or Children")
	}

	lim := &planner.LimitNode{Child: nil}
	if lim.Name() != "Limit" || len(lim.Children()) != 0 {
		t.Errorf("unexpected LimitNode Name or Children")
	}

	filter := &planner.FilterNode{Child: nil}
	if filter.Name() != "Filter" || len(filter.Children()) != 0 {
		t.Errorf("unexpected FilterNode Name or Children")
	}

	hashAgg := &planner.HashAggregateNode{Child: nil}
	if hashAgg.Name() != "HashAggregate" || len(hashAgg.Children()) != 0 {
		t.Errorf("unexpected HashAggregateNode Name or Children")
	}

	join := &planner.HashJoinNode{Left: nil, Right: nil}
	if join.Name() != "HashJoin" || len(join.Children()) != 0 {
		t.Errorf("unexpected HashJoinNode Name or Children")
	}

	// Exercise formatExpr and foldConstantExpr coverage:
	// Binary float operations
	opt := planner.NewOptimizer()
	cases := []struct {
		op interfaces.BinaryOp
		l  float64
		r  float64
	}{
		{interfaces.OpSub, 5.0, 3.0},
		{interfaces.OpMul, 2.0, 4.0},
		{interfaces.OpDiv, 10.0, 2.0},
		{interfaces.OpDiv, 10.0, 0.0}, // div by zero
		{interfaces.OpLt, 1.0, 2.0},
		{interfaces.OpLtEq, 2.0, 2.0},
		{interfaces.OpGt, 3.0, 2.0},
		{interfaces.OpGtEq, 3.0, 3.0},
		{interfaces.OpNotEq, 1.0, 2.0},
	}
	for _, c := range cases {
		fScan := &planner.ScanNode{
			TableName: "t",
			Filter: interfaces.BinaryExpr{
				Op:    c.op,
				Left:  interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: c.l},
				Right: interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: c.r},
			},
		}
		_, _ = opt.Optimize(fScan)
	}

	// Binary int operations: sub, mul, div (zero & non-zero), lt, lteq, gt, gteq, noteq
	intCases := []struct {
		op interfaces.BinaryOp
		l  int64
		r  int64
	}{
		{interfaces.OpSub, 5, 3},
		{interfaces.OpMul, 2, 4},
		{interfaces.OpDiv, 10, 2},
		{interfaces.OpDiv, 10, 0},
		{interfaces.OpLt, 1, 2},
		{interfaces.OpLtEq, 2, 2},
		{interfaces.OpGt, 3, 2},
		{interfaces.OpGtEq, 3, 3},
		{interfaces.OpNotEq, 1, 2},
	}
	for _, c := range intCases {
		iScan := &planner.ScanNode{
			TableName: "t",
			Filter: interfaces.BinaryExpr{
				Op:    c.op,
				Left:  interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: c.l},
				Right: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: c.r},
			},
		}
		_, _ = opt.Optimize(iScan)
	}

	// Predicate pushdown into ScanNode when scan columns match
	cat := newMockCatalog()
	cat.tables["users"] = &interfaces.TableMetadata{
		Name:        "users",
		ColumnNames: []string{"id", "age", "status"},
		Schema:      []interfaces.DataType{interfaces.TypeInt64, interfaces.TypeInt64, interfaces.TypeVarChar},
		RowCount:    200,
	}
	p := planner.NewPlanner(opt)
	stmtPush := &interfaces.SelectStatement{
		FromTable: "users",
		FromAlias: "u",
		Where: interfaces.BinaryExpr{
			Op:    interfaces.OpGt,
			Left:  interfaces.ColumnRef{Table: "u", Name: "age"},
			Right: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(18)},
		},
		Projections: []interfaces.SelectItem{
			{Expr: interfaces.ColumnRef{Table: "u", Name: "id"}},
		},
	}
	pPlan, err := p.Plan(context.Background(), stmtPush, cat)
	if err != nil {
		t.Fatalf("Plan pushdown failed: %v", err)
	}
	expPush := p.Explain(pPlan, false, nil)
	if !strings.Contains(expPush, "filter=") {
		t.Errorf("expected filter pushed down into Scan, got %s", expPush)
	}

	// Format expressions and AST nodes: CaseExpr, CastExpr, UnaryExpr, etc.
	caseExpr := interfaces.CaseExpr{
		Operand: interfaces.ColumnRef{Name: "status"},
		Whens: []interfaces.CaseWhen{
			{
				When: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: "active"},
				Then: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(1)},
			},
		},
		Else: interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: int64(0)},
	}
	castExpr := interfaces.CastExpr{
		Expr:       interfaces.ColumnRef{Name: "id"},
		TargetType: interfaces.TypeVarChar,
	}
	stmtExprs := &interfaces.SelectStatement{
		FromTable: "users",
		Projections: []interfaces.SelectItem{
			{Expr: caseExpr, Alias: "is_active"},
			{Expr: castExpr, Alias: "id_str"},
			{Expr: interfaces.UnaryExpr{Op: "NOT", Operand: interfaces.ColumnRef{Name: "status"}}},
			{Expr: interfaces.BinaryExpr{Op: interfaces.OpLike, Left: interfaces.ColumnRef{Name: "status"}, Right: interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: "act%"}}},
		},
	}
	pPlan2, err := p.Plan(context.Background(), stmtExprs, cat)
	if err != nil {
		t.Fatalf("Plan with expressions failed: %v", err)
	}
	exp2 := p.Explain(pPlan2, false, nil)
	if !strings.Contains(exp2, "Project") {
		t.Errorf("expected Project node in explain output, got %s", exp2)
	}

	// Test Explain ANALYZE with multiple operators of same type
	t1Meta := &interfaces.TableMetadata{
		Name:        "t1",
		ColumnNames: []string{"a"},
		Schema:      []interfaces.DataType{interfaces.TypeInt64},
		RowCount:    10,
	}
	t2Meta := &interfaces.TableMetadata{
		Name:        "t2",
		ColumnNames: []string{"b"},
		Schema:      []interfaces.DataType{interfaces.TypeInt64},
		RowCount:    20,
	}
	catMulti := newMockCatalog()
	catMulti.tables["t1"] = t1Meta
	catMulti.tables["t2"] = t2Meta
	stmtMulti := &interfaces.SelectStatement{
		FromTable: "t1",
		Joins: []interfaces.JoinClause{
			{
				Type:      interfaces.JoinInner,
				Table:     "t2",
				Condition: interfaces.BinaryExpr{Op: interfaces.OpEq, Left: interfaces.ColumnRef{Table: "t1", Name: "a"}, Right: interfaces.ColumnRef{Table: "t2", Name: "b"}},
			},
		},
		Projections: []interfaces.SelectItem{
			{Expr: interfaces.ColumnRef{Table: "t1", Name: "a"}},
		},
	}
	planMulti, err := p.Plan(context.Background(), stmtMulti, catMulti)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Supply two distinct Scan metrics
	multiMetrics := []interfaces.OperatorMetrics{
		{
			OperatorName: "Scan",
			RowsEmitted:  111,
			BatchesCount: 1,
			Duration:     time.Millisecond,
		},
		{
			OperatorName: "Scan",
			RowsEmitted:  222,
			BatchesCount: 2,
			Duration:     2 * time.Millisecond,
		},
	}
	explainMulti := p.Explain(planMulti, true, multiMetrics)
	if !strings.Contains(explainMulti, "rows=111") || !strings.Contains(explainMulti, "rows=222") {
		t.Errorf("expected multiple Scan metrics to be reported distinctly, got:\n%s", explainMulti)
	}

	// Also test passing unique node IDs directly in metrics
	var scanIDs []string
	var collectScanIDs func(n interfaces.PhysicalPlanNode)
	collectScanIDs = func(n interfaces.PhysicalPlanNode) {
		if n == nil {
			return
		}
		if s, ok := n.(*planner.ScanNode); ok {
			scanIDs = append(scanIDs, s.ID())
		}
		for _, c := range n.Children() {
			collectScanIDs(c)
		}
	}
	collectScanIDs(planMulti)
	if len(scanIDs) == 2 {
		idMetrics := []interfaces.OperatorMetrics{
			{
				OperatorName: scanIDs[0],
				RowsEmitted:  555,
				BatchesCount: 5,
				Duration:     5 * time.Millisecond,
			},
			{
				OperatorName: scanIDs[1],
				RowsEmitted:  777,
				BatchesCount: 7,
				Duration:     7 * time.Millisecond,
			},
		}
		explainIDs := p.Explain(planMulti, true, idMetrics)
		if !strings.Contains(explainIDs, "rows=555") || !strings.Contains(explainIDs, "rows=777") {
			t.Errorf("expected metrics matched by unique ID, got:\n%s", explainIDs)
		}
	}
}
