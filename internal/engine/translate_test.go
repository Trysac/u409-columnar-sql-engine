package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/engine"
)

// queryRows runs sql and returns every row scanned into []any.
func queryRows(t *testing.T, eng *engine.Engine, sql string) [][]any {
	t.Helper()
	cur, err := eng.Query(context.Background(), sql)
	if err != nil {
		t.Fatalf("query %q failed: %v", sql, err)
	}
	defer cur.Close()
	var rows [][]any
	for cur.Next() {
		n := len(cur.CurrentBatch().Columns)
		vals := make([]any, n)
		ptrs := make([]any, n)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := cur.Scan(ptrs...); err != nil {
			t.Fatalf("scan %q failed: %v", sql, err)
		}
		rows = append(rows, vals)
	}
	return rows
}

func errCode(err error) string {
	var ee *interfaces.EngineError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ""
}

func TestPlanTranslation(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()

	cases := []struct {
		name string
		sql  string
		want [][]any
	}{
		{
			name: "join keeps right column types and resolves qualified duplicates",
			sql:  "SELECT r.id, r.score, l.id FROM l JOIN r ON r.id = l.id ORDER BY l.id",
			want: [][]any{{int64(1), 95.0, int64(1)}, {int64(2), 80.0, int64(2)}},
		},
		{
			name: "left join keeps unmatched and null-key left rows",
			sql:  "SELECT l.name, r.score FROM l LEFT JOIN r ON l.id = r.id ORDER BY l.name",
			want: [][]any{{"alice", 95.0}, {"bob", 80.0}, {"charlie", nil}, {"david", nil}},
		},
		{
			name: "filter on a column that is not selected",
			sql:  "SELECT id FROM t WHERE val IS NULL",
			want: [][]any{{int64(3)}},
		},
		{
			name: "order by a column that is not selected",
			sql:  "SELECT name FROM t WHERE id <= 3 ORDER BY id DESC",
			want: [][]any{{"gamma"}, {"beta"}, {"alpha"}},
		},
		{
			name: "order by a select alias",
			sql:  "SELECT id, val * 2 AS doubled FROM t WHERE val IS NOT NULL ORDER BY doubled DESC LIMIT 2",
			want: [][]any{{int64(5), 100.0}, {int64(4), 80.5}},
		},
		{
			name: "star expands every column in table order",
			sql:  "SELECT * FROM sales WHERE id = 3",
			want: [][]any{{int64(3), "west", 150.0}},
		},
		{
			name: "aggregate before group column follows select order",
			sql:  "SELECT COUNT(*), grp FROM sales GROUP BY grp ORDER BY grp",
			want: [][]any{{int64(2), "east"}, {int64(1), "north"}, {int64(1), "west"}},
		},
		{
			name: "nested aggregate expression and having on an unselected aggregate",
			sql:  "SELECT grp, SUM(amount) / COUNT(*) AS mean FROM sales GROUP BY grp HAVING COUNT(id) > 1",
			want: [][]any{{"east", 125.0}},
		},
		{
			name: "order by an aggregate and by an aggregate alias",
			sql:  "SELECT grp, MAX(amount) AS top FROM sales GROUP BY grp ORDER BY top DESC, grp LIMIT 2",
			want: [][]any{{"east", 150.0}, {"west", 150.0}},
		},
		{
			name: "integer sum and string min keep their types",
			sql:  "SELECT SUM(id), MIN(name), MAX(name) FROM t",
			want: [][]any{{int64(15), "alpha", "gamma"}},
		},
		{
			name: "offset without limit",
			sql:  "SELECT id FROM t ORDER BY id OFFSET 3",
			want: [][]any{{int64(4)}, {int64(5)}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := queryRows(t, eng, tc.sql)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s\n got  %#v\n want %#v", tc.sql, got, tc.want)
			}
		})
	}
}

func TestQueryErrors(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()
	ctx := context.Background()

	cases := []struct {
		name string
		sql  string
		code string
	}{
		{"query over 100000 chars", "SELECT 1" + strings.Repeat(" ", 100000), interfaces.ErrQueryTooLarge},
		{"unknown table", "SELECT id FROM missing", interfaces.ErrTableNotFound},
		{"unknown column", "SELECT nope FROM t", interfaces.ErrColumnNotFound},
		{"non-equality join", "SELECT l.id FROM l JOIN r ON l.id > r.id", interfaces.ErrUnsupportedStatement},
		{"division by zero", "SELECT id / 0 FROM t", interfaces.ErrDivisionByZero},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Query(ctx, tc.sql)
			if errCode(err) != tc.code {
				t.Fatalf("expected code %q, got %v", tc.code, err)
			}
			_, err = eng.Explain(ctx, tc.sql, true)
			if errCode(err) != tc.code {
				t.Fatalf("explain: expected code %q, got %v", tc.code, err)
			}
		})
	}

	exact := "SELECT 1" + strings.Repeat(" ", engine.MaxQueryLength-len("SELECT 1"))
	if _, err := eng.Query(ctx, exact); err != nil {
		t.Fatalf("query of exactly %d chars should run: %v", engine.MaxQueryLength, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := eng.Query(cancelled, "SELECT id FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestMaxMemoryLimit(t *testing.T) {
	eng := engine.Open(interfaces.Options{MaxMemory: 16})
	defer eng.Close()
	if err := eng.RegisterCSV("t", strings.NewReader("id\n1\n2\n3\n"), []interfaces.DataType{interfaces.TypeInt64}); err != nil {
		t.Fatal(err)
	}
	_, err := eng.Query(context.Background(), "SELECT id FROM t")
	if errCode(err) != interfaces.ErrLimitExceeded {
		t.Fatalf("expected limit_exceeded, got %v", err)
	}
}

func TestClosedEngine(t *testing.T) {
	eng := setupTestEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("second Close should be a no-op: %v", err)
	}
	ctx := context.Background()
	if _, err := eng.Query(ctx, "SELECT 1"); err == nil {
		t.Fatal("expected error from Query on closed engine")
	}
	if _, err := eng.Explain(ctx, "SELECT 1", false); err == nil {
		t.Fatal("expected error from Explain on closed engine")
	}
	if err := eng.RegisterCSV("x", strings.NewReader("a\n1\n"), []interfaces.DataType{interfaces.TypeInt64}); err == nil {
		t.Fatal("expected error from RegisterCSV on closed engine")
	}
}

func TestExplain(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()
	ctx := context.Background()

	plain, err := eng.Explain(ctx, "SELECT l.name FROM l JOIN r ON l.id = r.id WHERE r.score > 90.0 ORDER BY l.name", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Sort", "Project", "Filter", "HashJoin: type=INNER", "Scan: table=l", "Scan: table=r"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain explain missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "rows=") {
		t.Errorf("plain explain should not carry metrics:\n%s", plain)
	}

	analyzed, err := eng.Explain(ctx, "SELECT grp, COUNT(*) FROM sales WHERE amount > 100.0 GROUP BY grp", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HashAggregate", "rows=2, batches=1", "Scan: table=sales", "filter="} {
		if !strings.Contains(analyzed, want) {
			t.Errorf("analyzed explain missing %q:\n%s", want, analyzed)
		}
	}
}

func TestCursorScanDestinations(t *testing.T) {
	eng := setupTestEngine(t)
	defer eng.Close()
	ctx := context.Background()

	cur, err := eng.Query(ctx, "SELECT id, name, val, id > 4 FROM t WHERE id = 5")
	if err != nil {
		t.Fatal(err)
	}
	if err := cur.Scan(new(any), new(any), new(any), new(any)); err == nil {
		t.Fatal("Scan before Next should fail")
	}
	if !cur.Next() {
		t.Fatal("expected a row")
	}
	var (
		id   int
		name *string
		val  *float64
		big  bool
	)
	if err := cur.Scan(&id, &name, &val, &big); err != nil {
		t.Fatal(err)
	}
	if id != 5 || name != nil || val == nil || *val != 50.0 || !big {
		t.Fatalf("unexpected values id=%d name=%v val=%v big=%v", id, name, val, big)
	}
	var s string
	var n int64
	var f float64
	var bp *bool
	var ip *int64
	var sp *string
	if err := cur.Scan(&n, &s, &f, &bp); err != nil || s != "" || bp == nil || !*bp {
		t.Fatalf("NULL into *string should give \"\": s=%q err=%v", s, err)
	}
	if err := cur.Scan(&ip, &sp, &f, &big); err != nil || ip == nil || *ip != 5 || sp != nil {
		t.Fatalf("pointer scan failed: err=%v", err)
	}
	if err := cur.Scan(&s, &s, &f, &big); errCode(err) != interfaces.ErrTypeMismatch {
		t.Fatalf("int column into *string should be type_mismatch, got %v", err)
	}
	if err := cur.Scan(&n, &s, &n, &big); errCode(err) != interfaces.ErrTypeMismatch {
		t.Fatalf("float column into *int64 should be type_mismatch, got %v", err)
	}
	if err := cur.Scan(&id); errCode(err) != interfaces.ErrTypeMismatch {
		t.Fatalf("wrong destination count should be type_mismatch, got %v", err)
	}
	if err := cur.Scan(new(uint8), &s, &f, &big); errCode(err) != interfaces.ErrTypeMismatch {
		t.Fatalf("unsupported destination should be type_mismatch, got %v", err)
	}
	if cur.Next() {
		t.Fatal("expected a single row")
	}
	if err := cur.Close(); err != nil {
		t.Fatal(err)
	}
	if cur.Next() || cur.CurrentBatch() != nil {
		t.Fatal("closed cursor should be exhausted")
	}
	if err := cur.Scan(&id, &name, &val, &big); err == nil {
		t.Fatal("Scan on closed cursor should fail")
	}
}

func TestDefaultsAndBatchSize(t *testing.T) {
	eng := engine.Open(interfaces.Options{BatchSize: 2})
	defer eng.Close()
	if err := eng.RegisterCSV("t", strings.NewReader("id\n1\n2\n3\n4\n5\n"), []interfaces.DataType{interfaces.TypeInt64}); err != nil {
		t.Fatal(err)
	}
	cur, err := eng.Query(context.Background(), "SELECT id FROM t")
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	var last *interfaces.RecordBatch
	for cur.Next() {
		if b := cur.CurrentBatch(); b != last {
			sizes = append(sizes, b.Length)
			last = b
		}
	}
	if !reflect.DeepEqual(sizes, []int{2, 2, 1}) {
		t.Fatalf("expected batches [2 2 1], got %v", sizes)
	}
}
