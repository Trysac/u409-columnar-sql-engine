package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
	"github.com/Trysac/u409-columnar-sql-engine/internal/exec"
	"github.com/Trysac/u409-columnar-sql-engine/internal/planner"
	"github.com/Trysac/u409-columnar-sql-engine/internal/storage"
)

// builder translates planner nodes into internal/exec operators. exec.ExecutionEngine.Execute
// predates the planner and cannot do this translation, so it lives here.
//
// Scanned columns are renamed to "alias.column" so qualified references (l.id, r.id) resolve
// unambiguously after a join; unqualified references still match by column suffix.
type builder struct {
	stmt      *interfaces.SelectStatement
	catalog   *storage.Catalog
	evaluator interfaces.VectorEvaluator
	batchSize int
	analyze   bool
	now       func() time.Time

	// agg is set once the HashAggregate node is translated; expressions of the operators above
	// it (HAVING, ORDER BY, final projection) are rewritten to reference its output columns.
	agg     *aggRewrite
	metered []*meteredOp
}

func (b *builder) build(ctx context.Context, plan interfaces.PhysicalPlanNode) (interfaces.Operator, error) {
	op, err := b.node(ctx, plan)
	if err != nil {
		return nil, err
	}
	if b.agg == nil {
		return op, nil
	}
	// The planner's HashAggregate emits group columns then top-level aggregates; project the
	// SELECT list on top so column order, nested aggregates and aliases follow the statement.
	items := make([]interfaces.SelectItem, len(b.stmt.Projections))
	for i, item := range b.stmt.Projections {
		items[i] = interfaces.SelectItem{Expr: b.agg.rewrite(item.Expr, false), Alias: item.Alias}
	}
	return exec.NewProjectionOperator(op, items, b.evaluator), nil
}

func (b *builder) node(ctx context.Context, plan interfaces.PhysicalPlanNode) (interfaces.Operator, error) {
	if plan == nil {
		return exec.NewValuesOperator(), nil
	}
	var op interfaces.Operator
	var err error
	switch n := plan.(type) {
	case *planner.ScanNode:
		op, err = b.scan(ctx, n)
	case *planner.FilterNode:
		op, err = b.filter(ctx, n)
	case *planner.ProjectNode:
		op, err = b.project(ctx, n)
	case *planner.HashAggregateNode:
		op, err = b.aggregate(ctx, n)
	case *planner.HashJoinNode:
		op, err = b.join(ctx, n)
	case *planner.SortNode:
		op, err = b.sort(ctx, n)
	case *planner.LimitNode:
		op, err = b.limit(ctx, n)
	default:
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrUnsupportedStatement,
			Message: fmt.Sprintf("unsupported plan node %s", plan.Name()),
		}
	}
	if err != nil {
		return nil, err
	}
	return b.meter(plan, op), nil
}

// meter wraps op so EXPLAIN ANALYZE can report it under the plan node's ID.
func (b *builder) meter(plan interfaces.PhysicalPlanNode, op interfaces.Operator) interfaces.Operator {
	if !b.analyze {
		return op
	}
	id := plan.Name()
	if withID, ok := plan.(interface{ ID() string }); ok && withID.ID() != "" {
		id = withID.ID()
	}
	m := &meteredOp{Operator: op, id: id, now: b.now}
	b.metered = append(b.metered, m)
	return m
}

func (b *builder) scan(ctx context.Context, n *planner.ScanNode) (interfaces.Operator, error) {
	meta, err := b.catalog.GetTable(n.TableName)
	if err != nil {
		return nil, err
	}
	reader, err := b.catalog.ScanTable(ctx, n.TableName, b.scanColumns(meta), b.batchSize)
	if err != nil {
		return nil, err
	}
	alias := n.Alias
	if alias == "" {
		alias = n.TableName
	}
	var op interfaces.Operator = qualify(exec.NewScanOperator(reader), alias)
	if n.Filter != nil {
		op = exec.NewFilterOperator(op, n.Filter, b.evaluator)
	}
	return op, nil
}

// scanColumns returns the table columns the statement references, in table order. It reads the
// statement instead of ScanNode.Projections because the planner's projection pushdown drops
// columns that only a pushed-down scan filter or a HAVING/ORDER BY aggregate needs.
func (b *builder) scanColumns(meta *interfaces.TableMetadata) []string {
	refs := map[string]bool{}
	star := false
	for _, ref := range statementRefs(b.stmt) {
		if ref.Name == "*" {
			star = true
			continue
		}
		refs[strings.ToLower(ref.Name)] = true
	}
	if star || len(refs) == 0 {
		return nil // every column
	}
	var cols []string
	for _, c := range meta.ColumnNames {
		if refs[strings.ToLower(c)] {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	return cols
}

func (b *builder) filter(ctx context.Context, n *planner.FilterNode) (interfaces.Operator, error) {
	child, err := b.node(ctx, n.Child)
	if err != nil {
		return nil, err
	}
	cond := n.Condition
	if b.agg != nil {
		cond = b.agg.rewrite(cond, true)
	}
	return exec.NewFilterOperator(child, cond, b.evaluator), nil
}

func (b *builder) projectItems(n *planner.ProjectNode, child interfaces.Operator) []interfaces.SelectItem {
	names := n.ColumnNames()
	var items []interfaces.SelectItem
	for i, expr := range n.Exprs {
		if ref, ok := expr.(interfaces.ColumnRef); ok && ref.Name == "*" {
			items = append(items, expandStar(ref.Table, child.ColumnNames())...)
			continue
		}
		alias := ""
		if i < len(names) {
			alias = names[i]
		}
		items = append(items, interfaces.SelectItem{Expr: expr, Alias: alias})
	}
	return items
}

func (b *builder) project(ctx context.Context, n *planner.ProjectNode) (interfaces.Operator, error) {
	child, err := b.node(ctx, n.Child)
	if err != nil {
		return nil, err
	}
	return exec.NewProjectionOperator(child, b.projectItems(n, child), b.evaluator), nil
}

func (b *builder) aggregate(ctx context.Context, n *planner.HashAggregateNode) (interfaces.Operator, error) {
	child, err := b.node(ctx, n.Child)
	if err != nil {
		return nil, err
	}
	names := n.ColumnNames()
	rw := &aggRewrite{aliases: map[string]interfaces.Expr{}}
	var cols []string
	var schema []interfaces.DataType
	for i, g := range n.GroupBy {
		name := fmt.Sprintf("__group_%d", i)
		if i < len(names) {
			name = names[i]
		}
		rw.groups = append(rw.groups, g)
		rw.groupNames = append(rw.groupNames, name)
		cols = append(cols, name)
		schema = append(schema, staticType(g, child))
	}

	// Top-level aggregates keep the planner's names; aggregates nested in expressions or only
	// present in HAVING/ORDER BY are added, because the planner collects top-level ones only.
	calls := append([]interfaces.AggregateCall{}, n.Aggregates...)
	for _, c := range statementAggregates(b.stmt) {
		if !containsCall(calls, c) {
			calls = append(calls, c)
		}
	}
	var execCalls []interfaces.AggregateCall
	for i, c := range calls {
		name := fmt.Sprintf("__agg_%d", i)
		if idx := len(n.GroupBy) + i; i < len(n.Aggregates) && idx < len(names) {
			name = names[idx]
		}
		rw.calls = append(rw.calls, c)
		rw.callNames = append(rw.callNames, name)
		norm := normalizeCall(c)
		execCalls = append(execCalls, norm)
		cols = append(cols, name)
		schema = append(schema, aggregateType(norm, child))
	}
	for _, item := range b.stmt.Projections {
		if item.Alias != "" {
			rw.aliases[item.Alias] = item.Expr
		}
	}
	b.agg = rw
	return exec.NewHashAggregateOperator(child, n.GroupBy, execCalls, cols, schema, b.evaluator), nil
}

func (b *builder) join(ctx context.Context, n *planner.HashJoinNode) (interfaces.Operator, error) {
	left, err := b.node(ctx, n.Left)
	if err != nil {
		return nil, err
	}
	right, err := b.node(ctx, n.Right)
	if err != nil {
		return nil, err
	}
	leftKey, rightKey, ok := joinKeys(n.Condition, left.ColumnNames(), right.ColumnNames())
	if !ok {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrUnsupportedStatement,
			Message: "only single equality join conditions between both sides are supported",
		}
	}
	cols := append(append([]string{}, left.ColumnNames()...), right.ColumnNames()...)
	schema := append(append([]interfaces.DataType{}, left.Schema()...), right.Schema()...)
	return exec.NewHashJoinOperator(left, right, n.JoinType, leftKey, rightKey, cols, schema, b.evaluator, b.batchSize), nil
}

func (b *builder) sort(ctx context.Context, n *planner.SortNode) (interfaces.Operator, error) {
	if proj, ok := n.Child.(*planner.ProjectNode); ok {
		// Sort below the projection so ORDER BY may use columns that are not selected;
		// references to SELECT aliases are replaced by the aliased expression.
		child, err := b.node(ctx, proj.Child)
		if err != nil {
			return nil, err
		}
		order := make([]interfaces.OrderByItem, len(n.OrderBy))
		for i, item := range n.OrderBy {
			item.Expr = substituteAliases(item.Expr, b.stmt.Projections)
			order[i] = item
		}
		sorted := exec.NewSortOperator(child, order, b.evaluator)
		// Both operators keep the row count, so the projection line reports the sorted rows.
		return exec.NewProjectionOperator(b.meter(proj, sorted), b.projectItems(proj, child), b.evaluator), nil
	}
	child, err := b.node(ctx, n.Child)
	if err != nil {
		return nil, err
	}
	order := n.OrderBy
	if b.agg != nil {
		order = make([]interfaces.OrderByItem, len(n.OrderBy))
		for i, item := range n.OrderBy {
			item.Expr = b.agg.rewrite(item.Expr, true)
			order[i] = item
		}
	}
	return exec.NewSortOperator(child, order, b.evaluator), nil
}

func (b *builder) limit(ctx context.Context, n *planner.LimitNode) (interfaces.Operator, error) {
	child, err := b.node(ctx, n.Child)
	if err != nil {
		return nil, err
	}
	return exec.NewLimitOperator(child, n.Limit, n.Offset), nil
}

func (b *builder) metrics() []interfaces.OperatorMetrics {
	out := make([]interfaces.OperatorMetrics, len(b.metered))
	for i, m := range b.metered {
		out[i] = interfaces.OperatorMetrics{
			OperatorName: m.id,
			RowsEmitted:  m.rows,
			BatchesCount: m.batches,
			Duration:     m.dur,
		}
	}
	return out
}

// aggRewrite maps expressions above a HashAggregate onto its output columns.
type aggRewrite struct {
	groups     []interfaces.Expr
	groupNames []string
	calls      []interfaces.AggregateCall
	callNames  []string
	aliases    map[string]interfaces.Expr
}

func (r *aggRewrite) rewrite(expr interfaces.Expr, useAliases bool) interfaces.Expr {
	if expr == nil {
		return nil
	}
	for i, g := range r.groups {
		if reflect.DeepEqual(g, expr) {
			return interfaces.ColumnRef{Name: r.groupNames[i]}
		}
	}
	switch e := expr.(type) {
	case interfaces.AggregateCall:
		for i, c := range r.calls {
			if reflect.DeepEqual(c, e) {
				return interfaces.ColumnRef{Name: r.callNames[i]}
			}
		}
		return e
	case interfaces.ColumnRef:
		if useAliases && e.Table == "" && !r.isOutput(e.Name) {
			if target, ok := r.aliases[e.Name]; ok {
				return r.rewrite(target, false)
			}
		}
		return e
	case interfaces.BinaryExpr:
		e.Left = r.rewrite(e.Left, useAliases)
		e.Right = r.rewrite(e.Right, useAliases)
		return e
	case interfaces.UnaryExpr:
		e.Operand = r.rewrite(e.Operand, useAliases)
		return e
	case interfaces.CastExpr:
		e.Expr = r.rewrite(e.Expr, useAliases)
		return e
	case interfaces.CaseExpr:
		e.Operand = r.rewrite(e.Operand, useAliases)
		whens := make([]interfaces.CaseWhen, len(e.Whens))
		for i, w := range e.Whens {
			whens[i] = interfaces.CaseWhen{When: r.rewrite(w.When, useAliases), Then: r.rewrite(w.Then, useAliases)}
		}
		e.Whens = whens
		e.Else = r.rewrite(e.Else, useAliases)
		return e
	default:
		return expr
	}
}

func (r *aggRewrite) isOutput(name string) bool {
	for _, n := range r.groupNames {
		if n == name {
			return true
		}
	}
	for _, n := range r.callNames {
		if n == name {
			return true
		}
	}
	return false
}

// substituteAliases replaces unqualified references to SELECT aliases with their expressions.
func substituteAliases(expr interfaces.Expr, items []interfaces.SelectItem) interfaces.Expr {
	ref, ok := expr.(interfaces.ColumnRef)
	if !ok || ref.Table != "" {
		return expr
	}
	for _, item := range items {
		if item.Alias != "" && item.Alias == ref.Name {
			return item.Expr
		}
	}
	return expr
}

// joinKeys splits an equality condition into the key evaluated on each join side.
func joinKeys(cond interfaces.Expr, leftCols, rightCols []string) (interfaces.Expr, interfaces.Expr, bool) {
	eq, ok := cond.(interfaces.BinaryExpr)
	if !ok || eq.Op != interfaces.OpEq {
		return nil, nil, false
	}
	if resolvesAll(eq.Left, leftCols) && resolvesAll(eq.Right, rightCols) {
		return eq.Left, eq.Right, true
	}
	if resolvesAll(eq.Right, leftCols) && resolvesAll(eq.Left, rightCols) {
		return eq.Right, eq.Left, true
	}
	return nil, nil, false
}

func resolvesAll(expr interfaces.Expr, cols []string) bool {
	refs := exprRefs(expr)
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if columnIndex(ref, cols) < 0 {
			return false
		}
	}
	return true
}

// columnIndex mirrors the column resolution of exec.Evaluator.
func columnIndex(ref interfaces.ColumnRef, cols []string) int {
	for i, name := range cols {
		if name == ref.Name || (ref.Table != "" && name == ref.Table+"."+ref.Name) {
			return i
		}
		if dot := strings.LastIndex(name, "."); dot >= 0 && name[dot+1:] == ref.Name {
			if ref.Table == "" || strings.HasPrefix(name, ref.Table+".") {
				return i
			}
		}
	}
	return -1
}

func expandStar(table string, cols []string) []interfaces.SelectItem {
	var items []interfaces.SelectItem
	for _, name := range cols {
		qual, col := "", name
		if dot := strings.LastIndex(name, "."); dot >= 0 {
			qual, col = name[:dot], name[dot+1:]
		}
		if table != "" && qual != table {
			continue
		}
		items = append(items, interfaces.SelectItem{Expr: interfaces.ColumnRef{Table: qual, Name: col}, Alias: col})
	}
	return items
}

// normalizeCall upper-cases the function name and turns COUNT(*) into the argument-less form
// exec.HashAggregateOperator counts rows with.
func normalizeCall(c interfaces.AggregateCall) interfaces.AggregateCall {
	c.Func = strings.ToUpper(c.Func)
	if len(c.Args) == 1 {
		if ref, ok := c.Args[0].(interfaces.ColumnRef); ok && ref.Name == "*" {
			c.Args = nil
		}
	}
	return c
}

func aggregateType(c interfaces.AggregateCall, child interfaces.Operator) interfaces.DataType {
	switch c.Func {
	case "COUNT":
		return interfaces.TypeInt64
	case "AVG":
		return interfaces.TypeFloat64
	}
	if len(c.Args) == 0 {
		return interfaces.TypeFloat64
	}
	t := staticType(c.Args[0], child)
	if c.Func == "SUM" && t != interfaces.TypeInt64 && t != interfaces.TypeDecimal {
		return interfaces.TypeFloat64
	}
	return t
}

// staticType predicts the type exec.Evaluator produces for expr over the child's columns.
func staticType(expr interfaces.Expr, child interfaces.Operator) interfaces.DataType {
	switch e := expr.(type) {
	case interfaces.ColumnRef:
		if idx := columnIndex(e, child.ColumnNames()); idx >= 0 && idx < len(child.Schema()) {
			return child.Schema()[idx]
		}
	case interfaces.LiteralExpr:
		return e.Type
	case interfaces.CastExpr:
		return e.TargetType
	case interfaces.UnaryExpr:
		if e.Op == "-" {
			return staticType(e.Operand, child)
		}
		return interfaces.TypeBool
	case interfaces.BinaryExpr:
		switch e.Op {
		case interfaces.OpAdd, interfaces.OpSub, interfaces.OpMul, interfaces.OpDiv:
			l, r := staticType(e.Left, child), staticType(e.Right, child)
			if l == interfaces.TypeFloat64 || r == interfaces.TypeFloat64 || l == interfaces.TypeDecimal || r == interfaces.TypeDecimal {
				return interfaces.TypeFloat64
			}
			return l
		default:
			return interfaces.TypeBool
		}
	case interfaces.CaseExpr:
		if len(e.Whens) > 0 {
			return staticType(e.Whens[0].Then, child)
		}
	}
	return interfaces.TypeVarChar
}

func containsCall(calls []interfaces.AggregateCall, c interfaces.AggregateCall) bool {
	for _, existing := range calls {
		if reflect.DeepEqual(existing, c) {
			return true
		}
	}
	return false
}

// statementAggregates lists aggregate calls in the SELECT list, HAVING and ORDER BY.
func statementAggregates(stmt *interfaces.SelectStatement) []interfaces.AggregateCall {
	var out []interfaces.AggregateCall
	var walk func(interfaces.Expr)
	walk = func(expr interfaces.Expr) {
		switch e := expr.(type) {
		case interfaces.AggregateCall:
			out = append(out, e)
		case interfaces.BinaryExpr:
			walk(e.Left)
			walk(e.Right)
		case interfaces.UnaryExpr:
			walk(e.Operand)
		case interfaces.CastExpr:
			walk(e.Expr)
		case interfaces.CaseExpr:
			walk(e.Operand)
			for _, w := range e.Whens {
				walk(w.When)
				walk(w.Then)
			}
			walk(e.Else)
		}
	}
	for _, item := range stmt.Projections {
		walk(item.Expr)
	}
	walk(stmt.Having)
	for _, o := range stmt.OrderBy {
		walk(o.Expr)
	}
	return out
}

// statementRefs lists every column reference in the statement.
func statementRefs(stmt *interfaces.SelectStatement) []interfaces.ColumnRef {
	var out []interfaces.ColumnRef
	for _, item := range stmt.Projections {
		out = append(out, exprRefs(item.Expr)...)
	}
	for _, j := range stmt.Joins {
		out = append(out, exprRefs(j.Condition)...)
	}
	out = append(out, exprRefs(stmt.Where)...)
	for _, g := range stmt.GroupBy {
		out = append(out, exprRefs(g)...)
	}
	out = append(out, exprRefs(stmt.Having)...)
	for _, o := range stmt.OrderBy {
		out = append(out, exprRefs(o.Expr)...)
	}
	return out
}

func exprRefs(expr interfaces.Expr) []interfaces.ColumnRef {
	switch e := expr.(type) {
	case interfaces.ColumnRef:
		return []interfaces.ColumnRef{e}
	case interfaces.BinaryExpr:
		return append(exprRefs(e.Left), exprRefs(e.Right)...)
	case interfaces.UnaryExpr:
		return exprRefs(e.Operand)
	case interfaces.CastExpr:
		return exprRefs(e.Expr)
	case interfaces.AggregateCall:
		var out []interfaces.ColumnRef
		for _, a := range e.Args {
			out = append(out, exprRefs(a)...)
		}
		return out
	case interfaces.CaseExpr:
		out := exprRefs(e.Operand)
		for _, w := range e.Whens {
			out = append(out, exprRefs(w.When)...)
			out = append(out, exprRefs(w.Then)...)
		}
		return append(out, exprRefs(e.Else)...)
	}
	return nil
}

// qualifiedOp renames a child's columns to "alias.column".
type qualifiedOp struct {
	interfaces.Operator
	names []string
}

func qualify(op interfaces.Operator, alias string) *qualifiedOp {
	src := op.ColumnNames()
	names := make([]string, len(src))
	for i, n := range src {
		names[i] = alias + "." + n
	}
	return &qualifiedOp{Operator: op, names: names}
}

func (q *qualifiedOp) ColumnNames() []string { return q.names }

// meteredOp records rows, batches and time spent for EXPLAIN ANALYZE.
type meteredOp struct {
	interfaces.Operator
	id      string
	now     func() time.Time
	rows    int64
	batches int64
	dur     time.Duration
}

func (m *meteredOp) Init(ctx context.Context) error {
	start := m.now()
	err := m.Operator.Init(ctx)
	m.dur += m.now().Sub(start)
	return err
}

func (m *meteredOp) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	start := m.now()
	batch, err := m.Operator.Next(ctx)
	m.dur += m.now().Sub(start)
	if batch != nil && batch.Length > 0 {
		m.rows += int64(batch.Length)
		m.batches++
	}
	return batch, err
}
