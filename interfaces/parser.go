package interfaces

// BinaryOp represents a binary operator token in an expression.
type BinaryOp int

const (
	OpEq BinaryOp = iota
	OpNotEq
	OpLt
	OpLtEq
	OpGt
	OpGtEq
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpAnd
	OpOr
	OpLike
)

// Expr represents an AST expression node.
type Expr interface {
	isExpr()
}

// ColumnRef references a column name with optional table qualification.
type ColumnRef struct {
	Table string
	Name  string
}

func (ColumnRef) isExpr() {}

// LiteralExpr represents a typed scalar constant or NULL.
type LiteralExpr struct {
	Type   DataType
	Value  any
	IsNull bool
}

func (LiteralExpr) isExpr() {}

// BinaryExpr represents binary operations (arithmetic, comparison, logical).
type BinaryExpr struct {
	Op    BinaryOp
	Left  Expr
	Right Expr
}

func (BinaryExpr) isExpr() {}

// UnaryExpr represents unary operations (NOT, IS NULL, IS NOT NULL).
type UnaryExpr struct {
	Op      string
	Operand Expr
}

func (UnaryExpr) isExpr() {}

// CaseWhen represents a single WHEN ... THEN ... arm in a CASE expression.
type CaseWhen struct {
	When Expr
	Then Expr
}

// CaseExpr represents CASE [expr] WHEN ... THEN ... ELSE ... END.
type CaseExpr struct {
	Operand Expr
	Whens   []CaseWhen
	Else    Expr
}

func (CaseExpr) isExpr() {}

// CastExpr represents CAST(expr AS type).
type CastExpr struct {
	Expr       Expr
	TargetType DataType
}

func (CastExpr) isExpr() {}

// AggregateCall represents an aggregate invocation (e.g. COUNT, SUM, AVG, MIN, MAX).
type AggregateCall struct {
	Func     string
	Distinct bool
	Args     []Expr
}

func (AggregateCall) isExpr() {}

// JoinType specifies the join semantics.
type JoinType int

const (
	JoinInner JoinType = iota
	JoinLeft
)

// JoinClause represents a JOIN relation with an ON condition.
type JoinClause struct {
	Type      JoinType
	Table     string
	Alias     string
	Condition Expr
}

// OrderByItem specifies an ORDER BY expression with ordering directions.
type OrderByItem struct {
	Expr       Expr
	Ascending  bool
	NullsFirst bool
}

// SelectItem represents a projected column or expression with an optional alias.
type SelectItem struct {
	Expr  Expr
	Alias string
}

// SelectStatement represents the AST for a parsed SELECT query.
type SelectStatement struct {
	Projections []SelectItem
	FromTable   string
	FromAlias   string
	Joins       []JoinClause
	Where       Expr
	GroupBy     []Expr
	Having      Expr
	OrderBy     []OrderByItem
	Limit       *int64
	Offset      *int64
}

// SQLParser parses SQL text into AST statements.
type SQLParser interface {
	Parse(sql string) (*SelectStatement, error)
}
