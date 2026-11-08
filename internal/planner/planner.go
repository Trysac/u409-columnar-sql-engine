package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// Planner implements query planning and EXPLAIN formatting.
type Planner struct {
	optimizer interfaces.RuleOptimizer
}

// NewPlanner returns a new QueryPlanner instance.
func NewPlanner(optimizer interfaces.RuleOptimizer) *Planner {
	if optimizer == nil {
		optimizer = NewOptimizer()
	}
	return &Planner{optimizer: optimizer}
}

// Plan transforms a parsed SelectStatement AST into an optimized PhysicalPlanNode.
func (p *Planner) Plan(ctx context.Context, stmt *interfaces.SelectStatement, catalog interfaces.TableCatalog) (interfaces.PhysicalPlanNode, error) {
	if stmt == nil {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrUnsupportedStatement,
			Message: "nil select statement",
		}
	}

	var currentPlan interfaces.PhysicalPlanNode

	// Table scan for FROM clause if present
	if stmt.FromTable != "" {
		tMeta, err := catalog.GetTable(stmt.FromTable)
		if err != nil {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrTableNotFound,
				Message: fmt.Sprintf("table '%s' not found: %v", stmt.FromTable, err),
			}
		}

		alias := stmt.FromAlias
		if alias == "" {
			alias = stmt.FromTable
		}

		cols := make([]string, len(tMeta.ColumnNames))
		copy(cols, tMeta.ColumnNames)
		schema := make([]interfaces.DataType, len(tMeta.Schema))
		copy(schema, tMeta.Schema)

		scan := &ScanNode{
			BaseNode: BaseNode{
				schema:      schema,
				columnNames: cols,
			},
			TableName:   stmt.FromTable,
			Alias:       alias,
			Projections: cols,
			RowCount:    tMeta.RowCount,
		}
		currentPlan = scan
	}

	// JOIN clauses
	for _, join := range stmt.Joins {
		jtMeta, err := catalog.GetTable(join.Table)
		if err != nil {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrTableNotFound,
				Message: fmt.Sprintf("table '%s' not found: %v", join.Table, err),
			}
		}

		alias := join.Alias
		if alias == "" {
			alias = join.Table
		}

		rCols := make([]string, len(jtMeta.ColumnNames))
		copy(rCols, jtMeta.ColumnNames)
		rSchema := make([]interfaces.DataType, len(jtMeta.Schema))
		copy(rSchema, jtMeta.Schema)

		rScan := &ScanNode{
			BaseNode: BaseNode{
				schema:      rSchema,
				columnNames: rCols,
			},
			TableName:   join.Table,
			Alias:       alias,
			Projections: rCols,
			RowCount:    jtMeta.RowCount,
		}

		// Join order by estimated cardinality (inner joins): smaller table as right/build
		left := currentPlan
		right := interfaces.PhysicalPlanNode(rScan)
		if join.Type == interfaces.JoinInner && currentPlan != nil {
			if leftScan, ok := currentPlan.(*ScanNode); ok {
				if leftScan.RowCount < rScan.RowCount {
					// Swap left and right so smaller cardinality relation is right
					left = rScan
					right = leftScan
				}
			}
		}

		var combinedCols []string
		var combinedSchema []interfaces.DataType
		if left != nil {
			combinedCols = append(combinedCols, left.ColumnNames()...)
			combinedSchema = append(combinedSchema, left.Schema()...)
		}
		combinedCols = append(combinedCols, right.ColumnNames()...)
		combinedSchema = append(combinedSchema, right.Schema()...)

		currentPlan = &HashJoinNode{
			BaseNode: BaseNode{
				schema:      combinedSchema,
				columnNames: combinedCols,
			},
			Left:      left,
			Right:     right,
			JoinType:  join.Type,
			Condition: join.Condition,
		}
	}

	// WHERE filter
	if stmt.Where != nil {
		var cols []string
		var schema []interfaces.DataType
		if currentPlan != nil {
			cols = currentPlan.ColumnNames()
			schema = currentPlan.Schema()
		}
		currentPlan = &FilterNode{
			BaseNode: BaseNode{
				schema:      schema,
				columnNames: cols,
			},
			Child:     currentPlan,
			Condition: stmt.Where,
		}
	}

	// Check if there are aggregate calls in SELECT or GROUP BY
	hasAggregates := false
	for _, item := range stmt.Projections {
		if hasAggCall(item.Expr) {
			hasAggregates = true
			break
		}
	}
	if len(stmt.GroupBy) > 0 {
		hasAggregates = true
	}

	if hasAggregates {
		var aggCalls []interfaces.AggregateCall
		var outCols []string
		var outSchema []interfaces.DataType

		for _, g := range stmt.GroupBy {
			if ref, ok := g.(interfaces.ColumnRef); ok {
				outCols = append(outCols, ref.Name)
				outSchema = append(outSchema, resolveType(ref, currentPlan))
			} else {
				outCols = append(outCols, formatExpr(g))
				outSchema = append(outSchema, interfaces.TypeVarChar)
			}
		}

		for _, item := range stmt.Projections {
			if call, ok := item.Expr.(interfaces.AggregateCall); ok {
				aggCalls = append(aggCalls, call)
				alias := item.Alias
				if alias == "" {
					alias = formatExpr(call)
				}
				outCols = append(outCols, alias)
				outSchema = append(outSchema, aggregateResultType(call, currentPlan))
			}
		}

		currentPlan = &HashAggregateNode{
			BaseNode: BaseNode{
				schema:      outSchema,
				columnNames: outCols,
			},
			Child:      currentPlan,
			GroupBy:    stmt.GroupBy,
			Aggregates: aggCalls,
		}
	} else if len(stmt.Projections) > 0 {
		// Projections
		var exprs []interfaces.Expr
		var outCols []string
		var outSchema []interfaces.DataType

		for _, item := range stmt.Projections {
			exprs = append(exprs, item.Expr)
			colName := item.Alias
			if colName == "" {
				switch e := item.Expr.(type) {
				case interfaces.ColumnRef:
					colName = e.Name
				default:
					colName = formatExpr(item.Expr)
				}
			}
			outCols = append(outCols, colName)
			outSchema = append(outSchema, resolveType(item.Expr, currentPlan))
		}

		currentPlan = &ProjectNode{
			BaseNode: BaseNode{
				schema:      outSchema,
				columnNames: outCols,
			},
			Child: currentPlan,
			Exprs: exprs,
		}
	}

	// HAVING filter
	if stmt.Having != nil {
		var cols []string
		var schema []interfaces.DataType
		if currentPlan != nil {
			cols = currentPlan.ColumnNames()
			schema = currentPlan.Schema()
		}
		currentPlan = &FilterNode{
			BaseNode: BaseNode{
				schema:      schema,
				columnNames: cols,
			},
			Child:     currentPlan,
			Condition: stmt.Having,
		}
	}

	// ORDER BY
	if len(stmt.OrderBy) > 0 {
		var cols []string
		var schema []interfaces.DataType
		if currentPlan != nil {
			cols = currentPlan.ColumnNames()
			schema = currentPlan.Schema()
		}
		currentPlan = &SortNode{
			BaseNode: BaseNode{
				schema:      schema,
				columnNames: cols,
			},
			Child:   currentPlan,
			OrderBy: stmt.OrderBy,
		}
	}

	// LIMIT / OFFSET
	if stmt.Limit != nil || stmt.Offset != nil {
		var cols []string
		var schema []interfaces.DataType
		if currentPlan != nil {
			cols = currentPlan.ColumnNames()
			schema = currentPlan.Schema()
		}
		currentPlan = &LimitNode{
			BaseNode: BaseNode{
				schema:      schema,
				columnNames: cols,
			},
			Child:  currentPlan,
			Limit:  stmt.Limit,
			Offset: stmt.Offset,
		}
	}

	// Apply optimizations
	if p.optimizer != nil && currentPlan != nil {
		var err error
		currentPlan, err = p.optimizer.Optimize(currentPlan)
		if err != nil {
			return nil, err
		}
	}

	assignNodeIDs(currentPlan)

	return currentPlan, nil
}

// assignNodeIDs assigns unique IDs to all nodes in the plan tree if not already set.
func assignNodeIDs(node interfaces.PhysicalPlanNode) {
	counter := 0
	var assign func(n interfaces.PhysicalPlanNode)
	assign = func(n interfaces.PhysicalPlanNode) {
		if n == nil {
			return
		}
		counter++
		if idSetter, ok := n.(interface{ SetID(string) }); ok {
			if idGetter, ok2 := n.(interface{ ID() string }); ok2 {
				if idGetter.ID() == "" {
					idSetter.SetID(fmt.Sprintf("%s_%d", n.Name(), counter))
				}
			} else {
				idSetter.SetID(fmt.Sprintf("%s_%d", n.Name(), counter))
			}
		}
		for _, child := range n.Children() {
			assign(child)
		}
	}
	assign(node)
}

// Explain renders the plan hierarchy and incorporates execution metrics when analyze is true.
func (p *Planner) Explain(plan interfaces.PhysicalPlanNode, analyze bool, metrics []interfaces.OperatorMetrics) string {
	if plan == nil {
		return "<nil>"
	}

	// Build a lookup map of operator name / ID to metrics, and support positional matching by operator name
	metricByID := make(map[string]interfaces.OperatorMetrics)
	metricByNameList := make(map[string][]interfaces.OperatorMetrics)
	for _, m := range metrics {
		metricByID[m.OperatorName] = m
		metricByNameList[m.OperatorName] = append(metricByNameList[m.OperatorName], m)
	}
	nameCursor := make(map[string]int)

	var sb strings.Builder
	p.formatTree(&sb, plan, 0, analyze, metricByID, metricByNameList, nameCursor)
	return sb.String()
}

func (p *Planner) formatTree(sb *strings.Builder, node interfaces.PhysicalPlanNode, depth int, analyze bool, metricByID map[string]interfaces.OperatorMetrics, metricByNameList map[string][]interfaces.OperatorMetrics, nameCursor map[string]int) {
	if node == nil {
		return
	}
	indent := strings.Repeat("  ", depth)
	line := fmt.Sprintf("%s%s", indent, node.Explain(analyze))

	if analyze {
		opName := node.Name()
		var nodeID string
		if idGetter, ok := node.(interface{ ID() string }); ok {
			nodeID = idGetter.ID()
		}

		var m interfaces.OperatorMetrics
		found := false

		// 1. Check exact match by unique node ID (if passed in metrics)
		if nodeID != "" {
			if metric, ok := metricByID[nodeID]; ok {
				m = metric
				found = true
			}
		}

		// 2. Fall back to matching by operator type sequentially across duplicate operator instances
		if !found {
			if list, ok := metricByNameList[opName]; ok {
				idx := nameCursor[opName]
				if idx < len(list) {
					m = list[idx]
					nameCursor[opName] = idx + 1
					found = true
				}
			}
		}

		if found {
			line = fmt.Sprintf("%s (rows=%d, batches=%d, duration=%v)", line, m.RowsEmitted, m.BatchesCount, m.Duration)
		} else {
			if scan, ok := node.(*ScanNode); ok {
				line = fmt.Sprintf("%s (rows=%d)", line, scan.RowCount)
			} else if _, ok := node.(*HashAggregateNode); ok {
				line = fmt.Sprintf("%s (rows=1)", line)
			}
		}
	}

	sb.WriteString(line)
	sb.WriteString("\n")

	for _, child := range node.Children() {
		p.formatTree(sb, child, depth+1, analyze, metricByID, metricByNameList, nameCursor)
	}
}

func hasAggCall(expr interfaces.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case interfaces.AggregateCall:
		return true
	case interfaces.BinaryExpr:
		return hasAggCall(e.Left) || hasAggCall(e.Right)
	case interfaces.UnaryExpr:
		return hasAggCall(e.Operand)
	case interfaces.CastExpr:
		return hasAggCall(e.Expr)
	default:
		return false
	}
}

func resolveType(expr interfaces.Expr, plan interfaces.PhysicalPlanNode) interfaces.DataType {
	if expr == nil {
		return interfaces.TypeVarChar
	}
	switch e := expr.(type) {
	case interfaces.ColumnRef:
		if plan != nil {
			cols := plan.ColumnNames()
			schemas := plan.Schema()
			for i, c := range cols {
				if c == e.Name && i < len(schemas) {
					return schemas[i]
				}
			}
		}
		return interfaces.TypeVarChar
	case interfaces.LiteralExpr:
		return e.Type
	case interfaces.CastExpr:
		return e.TargetType
	case interfaces.BinaryExpr:
		switch e.Op {
		case interfaces.OpEq, interfaces.OpNotEq, interfaces.OpLt, interfaces.OpLtEq, interfaces.OpGt, interfaces.OpGtEq, interfaces.OpAnd, interfaces.OpOr, interfaces.OpLike:
			return interfaces.TypeBool
		default:
			return resolveType(e.Left, plan)
		}
	case interfaces.UnaryExpr:
		if e.Op == "NOT" || e.Op == "IS NULL" || e.Op == "IS NOT NULL" {
			return interfaces.TypeBool
		}
		return resolveType(e.Operand, plan)
	default:
		return interfaces.TypeVarChar
	}
}

func aggregateResultType(call interfaces.AggregateCall, plan interfaces.PhysicalPlanNode) interfaces.DataType {
	fn := strings.ToUpper(call.Func)
	switch fn {
	case "COUNT":
		return interfaces.TypeInt64
	case "AVG":
		return interfaces.TypeFloat64
	case "SUM", "MIN", "MAX":
		if len(call.Args) > 0 {
			return resolveType(call.Args[0], plan)
		}
		return interfaces.TypeFloat64
	default:
		return interfaces.TypeVarChar
	}
}
