package planner

import (
	"math"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// DefaultOptimizer implements rule-based physical plan optimizations.
type DefaultOptimizer struct{}

// NewOptimizer creates a new DefaultOptimizer.
func NewOptimizer() *DefaultOptimizer {
	return &DefaultOptimizer{}
}

// Optimize applies constant folding, predicate pushdown, and projection pushdown.
func (o *DefaultOptimizer) Optimize(plan interfaces.PhysicalPlanNode) (interfaces.PhysicalPlanNode, error) {
	if plan == nil {
		return nil, nil
	}

	// Apply optimizations bottom-up or rule-by-rule
	plan = foldConstantsInPlan(plan)
	plan = pushdownPredicates(plan)
	plan = pushdownProjections(plan, nil)
	return plan, nil
}

// foldConstantsInPlan traverses physical plan and folds constant expressions.
func foldConstantsInPlan(node interfaces.PhysicalPlanNode) interfaces.PhysicalPlanNode {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *ScanNode:
		if n.Filter != nil {
			n.Filter = foldConstantExpr(n.Filter)
		}
		return n
	case *FilterNode:
		n.Child = foldConstantsInPlan(n.Child)
		n.Condition = foldConstantExpr(n.Condition)
		// If condition evaluates to constant literal TRUE, remove filter
		if lit, ok := n.Condition.(interfaces.LiteralExpr); ok && !lit.IsNull && lit.Type == interfaces.TypeBool {
			if b, ok := lit.Value.(bool); ok && b {
				return n.Child
			}
		}
		return n
	case *ProjectNode:
		n.Child = foldConstantsInPlan(n.Child)
		for i, e := range n.Exprs {
			n.Exprs[i] = foldConstantExpr(e)
		}
		return n
	case *HashAggregateNode:
		n.Child = foldConstantsInPlan(n.Child)
		for i, g := range n.GroupBy {
			n.GroupBy[i] = foldConstantExpr(g)
		}
		for i, agg := range n.Aggregates {
			for j, arg := range agg.Args {
				n.Aggregates[i].Args[j] = foldConstantExpr(arg)
			}
		}
		return n
	case *HashJoinNode:
		n.Left = foldConstantsInPlan(n.Left)
		n.Right = foldConstantsInPlan(n.Right)
		if n.Condition != nil {
			n.Condition = foldConstantExpr(n.Condition)
		}
		return n
	case *SortNode:
		n.Child = foldConstantsInPlan(n.Child)
		for i, ob := range n.OrderBy {
			n.OrderBy[i].Expr = foldConstantExpr(ob.Expr)
		}
		return n
	case *LimitNode:
		n.Child = foldConstantsInPlan(n.Child)
		return n
	default:
		return node
	}
}

// foldConstantExpr performs constant folding on AST expressions.
func foldConstantExpr(expr interfaces.Expr) interfaces.Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case interfaces.BinaryExpr:
		left := foldConstantExpr(e.Left)
		right := foldConstantExpr(e.Right)
		lLit, lOk := left.(interfaces.LiteralExpr)
		rLit, rOk := right.(interfaces.LiteralExpr)
		if lOk && rOk {
			if folded, ok := evaluateConstantBinary(e.Op, lLit, rLit); ok {
				return folded
			}
		}
		return interfaces.BinaryExpr{Op: e.Op, Left: left, Right: right}
	case interfaces.UnaryExpr:
		op := foldConstantExpr(e.Operand)
		if lit, ok := op.(interfaces.LiteralExpr); ok {
			if e.Op == "NOT" && lit.Type == interfaces.TypeBool && !lit.IsNull {
				if b, ok := lit.Value.(bool); ok {
					return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: !b, IsNull: false}
				}
			}
			if e.Op == "IS NULL" {
				return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lit.IsNull, IsNull: false}
			}
			if e.Op == "IS NOT NULL" {
				return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: !lit.IsNull, IsNull: false}
			}
		}
		return interfaces.UnaryExpr{Op: e.Op, Operand: op}
	default:
		return expr
	}
}

func evaluateConstantBinary(op interfaces.BinaryOp, left, right interfaces.LiteralExpr) (interfaces.LiteralExpr, bool) {
	// Three-valued boolean logic for AND / OR
	if op == interfaces.OpAnd {
		if (!left.IsNull && left.Type == interfaces.TypeBool && left.Value == false) ||
			(!right.IsNull && right.Type == interfaces.TypeBool && right.Value == false) {
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: false, IsNull: false}, true
		}
		if left.IsNull || right.IsNull {
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: nil, IsNull: true}, true
		}
		if left.Type == interfaces.TypeBool && right.Type == interfaces.TypeBool {
			lv := left.Value.(bool)
			rv := right.Value.(bool)
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv && rv, IsNull: false}, true
		}
	}
	if op == interfaces.OpOr {
		if (!left.IsNull && left.Type == interfaces.TypeBool && left.Value == true) ||
			(!right.IsNull && right.Type == interfaces.TypeBool && right.Value == true) {
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: true, IsNull: false}, true
		}
		if left.IsNull || right.IsNull {
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: nil, IsNull: true}, true
		}
		if left.Type == interfaces.TypeBool && right.Type == interfaces.TypeBool {
			lv := left.Value.(bool)
			rv := right.Value.(bool)
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv || rv, IsNull: false}, true
		}
	}

	if left.IsNull || right.IsNull {
		return interfaces.LiteralExpr{Type: left.Type, Value: nil, IsNull: true}, true
	}

	// Arithmetic on int64
	if left.Type == interfaces.TypeInt64 && right.Type == interfaces.TypeInt64 {
		lv := left.Value.(int64)
		rv := right.Value.(int64)
		switch op {
		case interfaces.OpAdd:
			return interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: lv + rv}, true
		case interfaces.OpSub:
			return interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: lv - rv}, true
		case interfaces.OpMul:
			return interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: lv * rv}, true
		case interfaces.OpDiv:
			if rv != 0 {
				return interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: lv / rv}, true
			}
		case interfaces.OpEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv == rv}, true
		case interfaces.OpNotEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv != rv}, true
		case interfaces.OpLt:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv < rv}, true
		case interfaces.OpLtEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv <= rv}, true
		case interfaces.OpGt:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv > rv}, true
		case interfaces.OpGtEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv >= rv}, true
		}
	}

	// Arithmetic and comparison on float64
	if left.Type == interfaces.TypeFloat64 && right.Type == interfaces.TypeFloat64 {
		lv := left.Value.(float64)
		rv := right.Value.(float64)
		switch op {
		case interfaces.OpAdd:
			return interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: lv + rv}, true
		case interfaces.OpSub:
			return interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: lv - rv}, true
		case interfaces.OpMul:
			return interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: lv * rv}, true
		case interfaces.OpDiv:
			if rv != 0 {
				return interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: lv / rv}, true
			}
		case interfaces.OpEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: math.Abs(lv-rv) < 1e-9}, true
		case interfaces.OpNotEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: math.Abs(lv-rv) >= 1e-9}, true
		case interfaces.OpLt:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv < rv}, true
		case interfaces.OpLtEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv <= rv}, true
		case interfaces.OpGt:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv > rv}, true
		case interfaces.OpGtEq:
			return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: lv >= rv}, true
		}
	}

	return interfaces.LiteralExpr{}, false
}

// pushdownPredicates pushes filter conditions down into ScanNode where possible.
func pushdownPredicates(node interfaces.PhysicalPlanNode) interfaces.PhysicalPlanNode {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *FilterNode:
		n.Child = pushdownPredicates(n.Child)
		if scan, ok := n.Child.(*ScanNode); ok {
			if canPushdownToScan(n.Condition, scan) {
				if scan.Filter == nil {
					scan.Filter = n.Condition
				} else {
					scan.Filter = interfaces.BinaryExpr{Op: interfaces.OpAnd, Left: scan.Filter, Right: n.Condition}
				}
				return scan
			}
		}
		return n
	case *ProjectNode:
		n.Child = pushdownPredicates(n.Child)
		return n
	case *HashAggregateNode:
		n.Child = pushdownPredicates(n.Child)
		return n
	case *HashJoinNode:
		n.Left = pushdownPredicates(n.Left)
		n.Right = pushdownPredicates(n.Right)
		return n
	case *SortNode:
		n.Child = pushdownPredicates(n.Child)
		return n
	case *LimitNode:
		n.Child = pushdownPredicates(n.Child)
		return n
	default:
		return node
	}
}

// canPushdownToScan checks if an expression only references columns available in ScanNode.
func canPushdownToScan(expr interfaces.Expr, scan *ScanNode) bool {
	cols := extractColumnRefs(expr)
	scanCols := make(map[string]bool)
	for _, col := range scan.columnNames {
		scanCols[col] = true
	}
	for _, c := range cols {
		if c.Table != "" && c.Table != scan.TableName && c.Table != scan.Alias {
			return false
		}
		if !scanCols[c.Name] {
			return false
		}
	}
	return true
}

// pushdownProjections pushes needed column projections down into ScanNode.
func pushdownProjections(node interfaces.PhysicalPlanNode, requiredCols map[string]bool) interfaces.PhysicalPlanNode {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *ScanNode:
		if len(requiredCols) > 0 {
			var newProjs []string
			var newSchema []interfaces.DataType
			for i, name := range n.columnNames {
				if requiredCols[name] {
					newProjs = append(newProjs, name)
					if i < len(n.schema) {
						newSchema = append(newSchema, n.schema[i])
					}
				}
			}
			if len(newProjs) > 0 {
				n.Projections = newProjs
				n.columnNames = newProjs
				n.schema = newSchema
			}
		}
		return n
	case *FilterNode:
		needed := make(map[string]bool)
		for k, v := range requiredCols {
			needed[k] = v
		}
		for _, ref := range extractColumnRefs(n.Condition) {
			needed[ref.Name] = true
		}
		n.Child = pushdownProjections(n.Child, needed)
		return n
	case *ProjectNode:
		needed := make(map[string]bool)
		for _, e := range n.Exprs {
			for _, ref := range extractColumnRefs(e) {
				needed[ref.Name] = true
			}
		}
		n.Child = pushdownProjections(n.Child, needed)
		return n
	case *HashAggregateNode:
		needed := make(map[string]bool)
		for _, g := range n.GroupBy {
			for _, ref := range extractColumnRefs(g) {
				needed[ref.Name] = true
			}
		}
		for _, agg := range n.Aggregates {
			for _, arg := range agg.Args {
				for _, ref := range extractColumnRefs(arg) {
					needed[ref.Name] = true
				}
			}
		}
		n.Child = pushdownProjections(n.Child, needed)
		return n
	case *HashJoinNode:
		needed := make(map[string]bool)
		for k, v := range requiredCols {
			needed[k] = v
		}
		for _, ref := range extractColumnRefs(n.Condition) {
			needed[ref.Name] = true
		}
		n.Left = pushdownProjections(n.Left, needed)
		n.Right = pushdownProjections(n.Right, needed)
		return n
	case *SortNode:
		needed := make(map[string]bool)
		for k, v := range requiredCols {
			needed[k] = v
		}
		for _, o := range n.OrderBy {
			for _, ref := range extractColumnRefs(o.Expr) {
				needed[ref.Name] = true
			}
		}
		n.Child = pushdownProjections(n.Child, needed)
		return n
	case *LimitNode:
		n.Child = pushdownProjections(n.Child, requiredCols)
		return n
	default:
		return node
	}
}

func extractColumnRefs(expr interfaces.Expr) []interfaces.ColumnRef {
	if expr == nil {
		return nil
	}
	var refs []interfaces.ColumnRef
	switch e := expr.(type) {
	case interfaces.ColumnRef:
		refs = append(refs, e)
	case interfaces.BinaryExpr:
		refs = append(refs, extractColumnRefs(e.Left)...)
		refs = append(refs, extractColumnRefs(e.Right)...)
	case interfaces.UnaryExpr:
		refs = append(refs, extractColumnRefs(e.Operand)...)
	case interfaces.CastExpr:
		refs = append(refs, extractColumnRefs(e.Expr)...)
	case interfaces.CaseExpr:
		if e.Operand != nil {
			refs = append(refs, extractColumnRefs(e.Operand)...)
		}
		for _, w := range e.Whens {
			refs = append(refs, extractColumnRefs(w.When)...)
			refs = append(refs, extractColumnRefs(w.Then)...)
		}
		if e.Else != nil {
			refs = append(refs, extractColumnRefs(e.Else)...)
		}
	case interfaces.AggregateCall:
		for _, arg := range e.Args {
			refs = append(refs, extractColumnRefs(arg)...)
		}
	}
	return refs
}
