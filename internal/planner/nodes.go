package planner

import (
	"fmt"
	"strings"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// BaseNode provides schema and column metadata for physical plan nodes.
type BaseNode struct {
	id          string
	schema      []interfaces.DataType
	columnNames []string
}

// ID returns the unique identifier of the node.
func (b *BaseNode) ID() string {
	return b.id
}

// SetID sets the unique identifier of the node.
func (b *BaseNode) SetID(id string) {
	b.id = id
}

// Schema returns the data types of the output columns.
func (b *BaseNode) Schema() []interfaces.DataType {
	return b.schema
}

// ColumnNames returns the names of the output columns.
func (b *BaseNode) ColumnNames() []string {
	return b.columnNames
}

// ScanNode represents a physical table scan operator.
type ScanNode struct {
	BaseNode
	TableName   string
	Alias       string
	Projections []string
	Filter      interfaces.Expr
	RowCount    int64
}

// Name returns operator name.
func (s *ScanNode) Name() string { return "Scan" }

// Children returns child physical plan nodes.
func (s *ScanNode) Children() []interfaces.PhysicalPlanNode { return nil }

// Explain returns textual explanation of ScanNode.
func (s *ScanNode) Explain(analyze bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Scan: table=%s", s.TableName))
	if s.Alias != "" && s.Alias != s.TableName {
		sb.WriteString(fmt.Sprintf(" as %s", s.Alias))
	}
	if len(s.Projections) > 0 {
		sb.WriteString(fmt.Sprintf(", projections=[%s]", strings.Join(s.Projections, ", ")))
	}
	if s.Filter != nil {
		sb.WriteString(fmt.Sprintf(", filter=%v", formatExpr(s.Filter)))
	}
	return sb.String()
}

// FilterNode represents a physical filter operator.
type FilterNode struct {
	BaseNode
	Child     interfaces.PhysicalPlanNode
	Condition interfaces.Expr
}

// Name returns operator name.
func (f *FilterNode) Name() string { return "Filter" }

// Children returns child physical plan nodes.
func (f *FilterNode) Children() []interfaces.PhysicalPlanNode {
	if f.Child == nil {
		return nil
	}
	return []interfaces.PhysicalPlanNode{f.Child}
}

// Explain returns textual explanation of FilterNode.
func (f *FilterNode) Explain(analyze bool) string {
	return fmt.Sprintf("Filter: condition=%s", formatExpr(f.Condition))
}

// ProjectNode represents a physical projection operator.
type ProjectNode struct {
	BaseNode
	Child interfaces.PhysicalPlanNode
	Exprs []interfaces.Expr
}

// Name returns operator name.
func (p *ProjectNode) Name() string { return "Project" }

// Children returns child physical plan nodes.
func (p *ProjectNode) Children() []interfaces.PhysicalPlanNode {
	if p.Child == nil {
		return nil
	}
	return []interfaces.PhysicalPlanNode{p.Child}
}

// Explain returns textual explanation of ProjectNode.
func (p *ProjectNode) Explain(analyze bool) string {
	parts := make([]string, len(p.Exprs))
	for i, e := range p.Exprs {
		parts[i] = formatExpr(e)
	}
	return fmt.Sprintf("Project: exprs=[%s]", strings.Join(parts, ", "))
}

// HashAggregateNode represents a hash-based aggregation operator.
type HashAggregateNode struct {
	BaseNode
	Child      interfaces.PhysicalPlanNode
	GroupBy    []interfaces.Expr
	Aggregates []interfaces.AggregateCall
}

// Name returns operator name.
func (h *HashAggregateNode) Name() string { return "HashAggregate" }

// Children returns child physical plan nodes.
func (h *HashAggregateNode) Children() []interfaces.PhysicalPlanNode {
	if h.Child == nil {
		return nil
	}
	return []interfaces.PhysicalPlanNode{h.Child}
}

// Explain returns textual explanation of HashAggregateNode.
func (h *HashAggregateNode) Explain(analyze bool) string {
	var groups []string
	for _, g := range h.GroupBy {
		groups = append(groups, formatExpr(g))
	}
	var aggs []string
	for _, a := range h.Aggregates {
		var args []string
		for _, arg := range a.Args {
			args = append(args, formatExpr(arg))
		}
		dist := ""
		if a.Distinct {
			dist = "DISTINCT "
		}
		aggs = append(aggs, fmt.Sprintf("%s(%s%s)", a.Func, dist, strings.Join(args, ", ")))
	}
	return fmt.Sprintf("HashAggregate: group=[%s], aggs=[%s]", strings.Join(groups, ", "), strings.Join(aggs, ", "))
}

// HashJoinNode represents a hash inner or left join operator.
type HashJoinNode struct {
	BaseNode
	Left      interfaces.PhysicalPlanNode
	Right     interfaces.PhysicalPlanNode
	JoinType  interfaces.JoinType
	Condition interfaces.Expr
}

// Name returns operator name.
func (h *HashJoinNode) Name() string { return "HashJoin" }

// Children returns child physical plan nodes.
func (h *HashJoinNode) Children() []interfaces.PhysicalPlanNode {
	var children []interfaces.PhysicalPlanNode
	if h.Left != nil {
		children = append(children, h.Left)
	}
	if h.Right != nil {
		children = append(children, h.Right)
	}
	return children
}

// Explain returns textual explanation of HashJoinNode.
func (h *HashJoinNode) Explain(analyze bool) string {
	jt := "INNER"
	if h.JoinType == interfaces.JoinLeft {
		jt = "LEFT"
	}
	return fmt.Sprintf("HashJoin: type=%s, on=%s", jt, formatExpr(h.Condition))
}

// SortNode represents a physical sort operator.
type SortNode struct {
	BaseNode
	Child   interfaces.PhysicalPlanNode
	OrderBy []interfaces.OrderByItem
}

// Name returns operator name.
func (s *SortNode) Name() string { return "Sort" }

// Children returns child physical plan nodes.
func (s *SortNode) Children() []interfaces.PhysicalPlanNode {
	if s.Child == nil {
		return nil
	}
	return []interfaces.PhysicalPlanNode{s.Child}
}

// Explain returns textual explanation of SortNode.
func (s *SortNode) Explain(analyze bool) string {
	var items []string
	for _, o := range s.OrderBy {
		dir := "ASC"
		if !o.Ascending {
			dir = "DESC"
		}
		nulls := "NULLS LAST"
		if o.NullsFirst {
			nulls = "NULLS FIRST"
		}
		items = append(items, fmt.Sprintf("%s %s %s", formatExpr(o.Expr), dir, nulls))
	}
	return fmt.Sprintf("Sort: order=[%s]", strings.Join(items, ", "))
}

// LimitNode represents a physical limit/offset operator.
type LimitNode struct {
	BaseNode
	Child  interfaces.PhysicalPlanNode
	Limit  *int64
	Offset *int64
}

// Name returns operator name.
func (l *LimitNode) Name() string { return "Limit" }

// Children returns child physical plan nodes.
func (l *LimitNode) Children() []interfaces.PhysicalPlanNode {
	if l.Child == nil {
		return nil
	}
	return []interfaces.PhysicalPlanNode{l.Child}
}

// Explain returns textual explanation of LimitNode.
func (l *LimitNode) Explain(analyze bool) string {
	lim := "none"
	off := int64(0)
	if l.Limit != nil {
		lim = fmt.Sprintf("%d", *l.Limit)
	}
	if l.Offset != nil {
		off = *l.Offset
	}
	return fmt.Sprintf("Limit: limit=%s, offset=%d", lim, off)
}

func formatExpr(expr interfaces.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case interfaces.ColumnRef:
		if e.Table != "" {
			return fmt.Sprintf("%s.%s", e.Table, e.Name)
		}
		return e.Name
	case interfaces.LiteralExpr:
		if e.IsNull {
			return "NULL"
		}
		if e.Type == interfaces.TypeVarChar {
			return fmt.Sprintf("'%v'", e.Value)
		}
		return fmt.Sprintf("%v", e.Value)
	case interfaces.BinaryExpr:
		opStr := "?"
		switch e.Op {
		case interfaces.OpEq:
			opStr = "="
		case interfaces.OpNotEq:
			opStr = "!="
		case interfaces.OpLt:
			opStr = "<"
		case interfaces.OpLtEq:
			opStr = "<="
		case interfaces.OpGt:
			opStr = ">"
		case interfaces.OpGtEq:
			opStr = ">="
		case interfaces.OpAdd:
			opStr = "+"
		case interfaces.OpSub:
			opStr = "-"
		case interfaces.OpMul:
			opStr = "*"
		case interfaces.OpDiv:
			opStr = "/"
		case interfaces.OpAnd:
			opStr = "AND"
		case interfaces.OpOr:
			opStr = "OR"
		case interfaces.OpLike:
			opStr = "LIKE"
		}
		return fmt.Sprintf("(%s %s %s)", formatExpr(e.Left), opStr, formatExpr(e.Right))
	case interfaces.UnaryExpr:
		return fmt.Sprintf("(%s %s)", e.Op, formatExpr(e.Operand))
	case interfaces.AggregateCall:
		var args []string
		for _, arg := range e.Args {
			args = append(args, formatExpr(arg))
		}
		dist := ""
		if e.Distinct {
			dist = "DISTINCT "
		}
		return fmt.Sprintf("%s(%s%s)", e.Func, dist, strings.Join(args, ", "))
	case interfaces.CastExpr:
		return fmt.Sprintf("CAST(%s AS type_%d)", formatExpr(e.Expr), e.TargetType)
	case interfaces.CaseExpr:
		return "CASE ... END"
	default:
		return fmt.Sprintf("%v", expr)
	}
}
