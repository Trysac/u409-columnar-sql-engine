package parser_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/parser"
)

func TestParserAcceptanceSelectFromWhere(t *testing.T) {
	p := parser.New()
	sql := "SELECT FROM WHERE"
	_, err := p.Parse(sql)
	if err == nil {
		t.Fatalf("expected error for SQL %q, got nil", sql)
	}
	var engineErr *interfaces.EngineError
	if !errors.As(err, &engineErr) {
		t.Fatalf("expected *interfaces.EngineError, got %T: %v", err, err)
	}
	if engineErr.Code != interfaces.ErrSyntaxError {
		t.Fatalf("expected code %q, got %q (message: %s)", interfaces.ErrSyntaxError, engineErr.Code, engineErr.Message)
	}
}

func TestParserSelectProjections(t *testing.T) {
	p := parser.New()
	stmt, err := p.Parse("SELECT a, t.b AS my_b, COUNT(*), SUM(v) FROM tbl AS t")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(stmt.Projections) != 4 {
		t.Fatalf("expected 4 projections, got %d", len(stmt.Projections))
	}
	if stmt.FromTable != "tbl" || stmt.FromAlias != "t" {
		t.Fatalf("expected FromTable tbl, alias t; got %s, %s", stmt.FromTable, stmt.FromAlias)
	}

	// Column a
	col1, ok := stmt.Projections[0].Expr.(interfaces.ColumnRef)
	if !ok || col1.Name != "a" || col1.Table != "" {
		t.Fatalf("unexpected projection 0: %+v", stmt.Projections[0])
	}

	// Column t.b AS my_b
	col2, ok := stmt.Projections[1].Expr.(interfaces.ColumnRef)
	if !ok || col2.Table != "t" || col2.Name != "b" || stmt.Projections[1].Alias != "my_b" {
		t.Fatalf("unexpected projection 1: %+v", stmt.Projections[1])
	}

	// COUNT(*)
	agg1, ok := stmt.Projections[2].Expr.(interfaces.AggregateCall)
	if !ok || agg1.Func != "COUNT" || len(agg1.Args) != 1 {
		t.Fatalf("unexpected projection 2: %+v", stmt.Projections[2])
	}

	// SUM(v)
	agg2, ok := stmt.Projections[3].Expr.(interfaces.AggregateCall)
	if !ok || agg2.Func != "SUM" || len(agg2.Args) != 1 {
		t.Fatalf("unexpected projection 3: %+v", stmt.Projections[3])
	}
}

func TestParserJoins(t *testing.T) {
	p := parser.New()
	sql := "SELECT l.id, r.val FROM l LEFT JOIN r ON l.id = r.id"
	stmt, err := p.Parse(sql)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(stmt.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(stmt.Joins))
	}
	join := stmt.Joins[0]
	if join.Type != interfaces.JoinLeft || join.Table != "r" {
		t.Fatalf("expected left join on r, got %v on %v", join.Type, join.Table)
	}
	binExpr, ok := join.Condition.(interfaces.BinaryExpr)
	if !ok || binExpr.Op != interfaces.OpEq {
		t.Fatalf("expected OpEq in join condition, got %+v", join.Condition)
	}
}

func TestParserWhereGroupByHavingOrderByLimitOffset(t *testing.T) {
	p := parser.New()
	sql := `SELECT g, SUM(v), COUNT(v), COUNT(*)
			FROM t
			WHERE v > 1.0 AND (g = 'a' OR g = 'b')
			GROUP BY g
			HAVING COUNT(v) >= 1
			ORDER BY g DESC NULLS LAST, SUM(v) ASC NULLS FIRST
			LIMIT 10 OFFSET 2;`

	stmt, err := p.Parse(sql)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if stmt.FromTable != "t" {
		t.Fatalf("expected table t, got %s", stmt.FromTable)
	}
	if stmt.Where == nil {
		t.Fatal("expected WHERE expression")
	}
	if len(stmt.GroupBy) != 1 {
		t.Fatalf("expected 1 GROUP BY expr, got %d", len(stmt.GroupBy))
	}
	if stmt.Having == nil {
		t.Fatal("expected HAVING expression")
	}
	if len(stmt.OrderBy) != 2 {
		t.Fatalf("expected 2 ORDER BY items, got %d", len(stmt.OrderBy))
	}
	if stmt.OrderBy[0].Ascending || stmt.OrderBy[0].NullsFirst {
		t.Fatalf("expected g DESC NULLS LAST")
	}
	if !stmt.OrderBy[1].Ascending || !stmt.OrderBy[1].NullsFirst {
		t.Fatalf("expected SUM(v) ASC NULLS FIRST")
	}
	if stmt.Limit == nil || *stmt.Limit != 10 {
		t.Fatalf("expected LIMIT 10, got %v", stmt.Limit)
	}
	if stmt.Offset == nil || *stmt.Offset != 2 {
		t.Fatalf("expected OFFSET 2, got %v", stmt.Offset)
	}
}

func TestParserExpressions(t *testing.T) {
	p := parser.New()

	// CASE expression
	caseSQL := "SELECT CASE WHEN a = 1 THEN 'one' WHEN a = 2 THEN 'two' ELSE 'other' END FROM t"
	stmtCase, err := p.Parse(caseSQL)
	if err != nil {
		t.Fatalf("failed to parse CASE: %v", err)
	}
	caseExpr, ok := stmtCase.Projections[0].Expr.(interfaces.CaseExpr)
	if !ok {
		t.Fatalf("expected CaseExpr, got %T", stmtCase.Projections[0].Expr)
	}
	if len(caseExpr.Whens) != 2 {
		t.Fatalf("expected 2 WHEN clauses, got %d", len(caseExpr.Whens))
	}

	// CAST expression
	castSQL := "SELECT CAST(x AS VARCHAR(50)), CAST(y AS INT64), CAST(z AS FLOAT64) FROM t"
	stmtCast, err := p.Parse(castSQL)
	if err != nil {
		t.Fatalf("failed to parse CAST: %v", err)
	}
	if len(stmtCast.Projections) != 3 {
		t.Fatalf("expected 3 CAST projections, got %d", len(stmtCast.Projections))
	}
	cast0, ok0 := stmtCast.Projections[0].Expr.(interfaces.CastExpr)
	if !ok0 || cast0.TargetType != interfaces.TypeVarChar {
		t.Fatalf("expected CAST to TypeVarChar, got %+v", stmtCast.Projections[0].Expr)
	}

	// Unary IS NULL, IS NOT NULL, NOT
	isNullSQL := "SELECT a IS NULL, b IS NOT NULL, NOT c FROM t"
	stmtNull, err := p.Parse(isNullSQL)
	if err != nil {
		t.Fatalf("failed to parse IS NULL / NOT: %v", err)
	}
	unary0, ok0 := stmtNull.Projections[0].Expr.(interfaces.UnaryExpr)
	if !ok0 || unary0.Op != "IS NULL" {
		t.Fatalf("expected IS NULL unary expr, got %+v", stmtNull.Projections[0].Expr)
	}
	unary1, ok1 := stmtNull.Projections[1].Expr.(interfaces.UnaryExpr)
	if !ok1 || unary1.Op != "IS NOT NULL" {
		t.Fatalf("expected IS NOT NULL unary expr, got %+v", stmtNull.Projections[1].Expr)
	}
	unary2, ok2 := stmtNull.Projections[2].Expr.(interfaces.UnaryExpr)
	if !ok2 || unary2.Op != "NOT" {
		t.Fatalf("expected NOT unary expr, got %+v", stmtNull.Projections[2].Expr)
	}
}

func TestParserPrecedenceAndArithmetic(t *testing.T) {
	p := parser.New()
	sql := "SELECT 1 + 2 * 3 - -4 / 2, 'abc' LIKE '%b%' FROM t"
	stmt, err := p.Parse(sql)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(stmt.Projections) != 2 {
		t.Fatalf("expected 2 projections, got %d", len(stmt.Projections))
	}

	likeExpr, ok := stmt.Projections[1].Expr.(interfaces.BinaryExpr)
	if !ok || likeExpr.Op != interfaces.OpLike {
		t.Fatalf("expected OpLike, got %+v", stmt.Projections[1].Expr)
	}
}

func TestParserDistinctAndNullLiteral(t *testing.T) {
	p := parser.New()
	sql := "SELECT COUNT(DISTINCT col), NULL AND FALSE, NULL OR TRUE, NULL = NULL"
	stmt, err := p.Parse(sql)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	agg, ok := stmt.Projections[0].Expr.(interfaces.AggregateCall)
	if !ok || !agg.Distinct {
		t.Fatalf("expected COUNT DISTINCT, got %+v", stmt.Projections[0].Expr)
	}
}

func TestParserSizeAndDepthLimits(t *testing.T) {
	p := parser.New()

	// Size limit exceeded
	tooBig := "SELECT " + strings.Repeat("a + ", 500000) + "1"
	_, err := p.Parse(tooBig)
	if err == nil {
		t.Fatal("expected query too large error")
	}
	var engErr *interfaces.EngineError
	if !errors.As(err, &engErr) || engErr.Code != interfaces.ErrQueryTooLarge {
		t.Fatalf("expected ErrQueryTooLarge, got %v", err)
	}

	// Depth limit exceeded
	nested := strings.Repeat("(", 150) + "1" + strings.Repeat(")", 150)
	_, err = p.Parse("SELECT " + nested)
	if err == nil {
		t.Fatal("expected depth exceeded error")
	}
	if !errors.As(err, &engErr) || engErr.Code != interfaces.ErrDepthExceeded {
		t.Fatalf("expected ErrDepthExceeded, got %v", err)
	}
}

func TestParserSyntaxErrors(t *testing.T) {
	p := parser.New()
	testCases := []string{
		"",
		"INSERT INTO t VALUES (1)",
		"SELECT",
		"SELECT 1 FROM",
		"SELECT 1 FROM t WHERE",
		"SELECT 1 FROM t GROUP BY",
		"SELECT 1 FROM t ORDER BY",
		"SELECT 1 FROM t LIMIT abc",
		"SELECT 1 FROM t OFFSET xyz",
		"SELECT 1 FROM t JOIN",
		"SELECT 1 FROM t JOIN t2",
		"SELECT CASE WHEN 1 THEN 2", // missing END
		"SELECT CAST(1 AS UNKNOWN_TYPE)",
		"SELECT a, FROM t",
		"SELECT 1 FROM t ORDER BY a NULLS FOO",
	}

	for _, tc := range testCases {
		_, err := p.Parse(tc)
		if err == nil {
			t.Errorf("expected error for SQL %q, got nil", tc)
			continue
		}
		var engErr *interfaces.EngineError
		if !errors.As(err, &engErr) {
			t.Errorf("for SQL %q: expected *interfaces.EngineError, got %T", tc, err)
			continue
		}
		if engErr.Code != interfaces.ErrSyntaxError {
			t.Errorf("for SQL %q: expected syntax_error, got %s", tc, engErr.Code)
		}
	}
}
