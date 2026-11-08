package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// Default limits.
const (
	DefaultMaxQueryLength = 1048576 // 1MB
	DefaultMaxDepth       = 128
)

// Parser parses SQL queries into AST nodes.
type Parser struct {
	tokens   []Token
	pos      int
	depth    int
	maxDepth int
}

// Ensure Parser implements interfaces.SQLParser
var _ interfaces.SQLParser = (*Parser)(nil)

// New creates a new Parser instance.
func New() *Parser {
	return &Parser{
		maxDepth: DefaultMaxDepth,
	}
}

// Parse parses the given SQL string into a SelectStatement AST.
func (p *Parser) Parse(sql string) (*interfaces.SelectStatement, error) {
	if len(sql) > DefaultMaxQueryLength {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrQueryTooLarge,
			Message: fmt.Sprintf("query size %d exceeds limit of %d", len(sql), DefaultMaxQueryLength),
		}
	}

	lexer := NewLexer(sql)
	var tokens []Token
	for {
		tok := lexer.NextToken()
		if tok.Type == TokenError {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrSyntaxError,
				Message: fmt.Sprintf("lexical error at pos %d: %s", tok.Pos, tok.Value),
			}
		}
		tokens = append(tokens, tok)
		if tok.Type == TokenEOF {
			break
		}
	}

	parser := &Parser{
		tokens:   tokens,
		pos:      0,
		depth:    0,
		maxDepth: p.maxDepth,
	}
	if parser.maxDepth <= 0 {
		parser.maxDepth = DefaultMaxDepth
	}

	return parser.parseSelect()
}

func (p *Parser) current() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) advance() Token {
	tok := p.current()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) match(types ...TokenType) bool {
	cur := p.current().Type
	for _, t := range types {
		if cur == t {
			p.advance()
			return true
		}
	}
	return false
}

func (p *Parser) expect(t TokenType, context string) (Token, error) {
	cur := p.current()
	if cur.Type != t {
		return Token{}, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("expected %v for %s at pos %d, got %s", t, context, cur.Pos, cur.Value),
		}
	}
	return p.advance(), nil
}

func (p *Parser) enterDepth() error {
	p.depth++
	if p.depth > p.maxDepth {
		return &interfaces.EngineError{
			Code:    interfaces.ErrDepthExceeded,
			Message: fmt.Sprintf("expression recursion depth limit %d exceeded", p.maxDepth),
		}
	}
	return nil
}

func (p *Parser) leaveDepth() {
	p.depth--
}

func (p *Parser) parseSelect() (*interfaces.SelectStatement, error) {
	if !p.match(TokenSelect) {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("expected SELECT statement, got %s", p.current().Value),
		}
	}

	stmt := &interfaces.SelectStatement{}

	// Projections
	projections, err := p.parseProjections()
	if err != nil {
		return nil, err
	}
	if len(projections) == 0 {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: "SELECT query must project at least one item",
		}
	}
	stmt.Projections = projections

	// FROM clause (optional in some queries like SELECT 1)
	if p.match(TokenFrom) {
		fromTable, fromAlias, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		stmt.FromTable = fromTable
		stmt.FromAlias = fromAlias

		// Optional JOINs
		joins, err := p.parseJoins()
		if err != nil {
			return nil, err
		}
		stmt.Joins = joins
	}

	// WHERE clause
	if p.match(TokenWhere) {
		whereExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = whereExpr
	}

	// GROUP BY clause
	if p.match(TokenGroup) {
		if _, err := p.expect(TokenBy, "GROUP BY"); err != nil {
			return nil, err
		}
		groupExprs, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		stmt.GroupBy = groupExprs
	}

	// HAVING clause
	if p.match(TokenHaving) {
		havingExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Having = havingExpr
	}

	// ORDER BY clause
	if p.match(TokenOrder) {
		if _, err := p.expect(TokenBy, "ORDER BY"); err != nil {
			return nil, err
		}
		orderItems, err := p.parseOrderByList()
		if err != nil {
			return nil, err
		}
		stmt.OrderBy = orderItems
	}

	// LIMIT and OFFSET clauses
	for {
		if p.match(TokenLimit) {
			numTok, err := p.expect(TokenNumber, "LIMIT")
			if err != nil {
				return nil, err
			}
			n, err := strconv.ParseInt(numTok.Value, 10, 64)
			if err != nil {
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrSyntaxError,
					Message: fmt.Sprintf("invalid LIMIT number %s", numTok.Value),
				}
			}
			stmt.Limit = &n
			continue
		}
		if p.match(TokenOffset) {
			numTok, err := p.expect(TokenNumber, "OFFSET")
			if err != nil {
				return nil, err
			}
			n, err := strconv.ParseInt(numTok.Value, 10, 64)
			if err != nil {
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrSyntaxError,
					Message: fmt.Sprintf("invalid OFFSET number %s", numTok.Value),
				}
			}
			stmt.Offset = &n
			continue
		}
		break
	}

	// Optional trailing semicolon
	p.match(TokenSemicolon)

	if p.current().Type != TokenEOF {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("unexpected token %s at pos %d", p.current().Value, p.current().Pos),
		}
	}

	return stmt, nil
}

func (p *Parser) parseProjections() ([]interfaces.SelectItem, error) {
	var items []interfaces.SelectItem
	for {
		item, err := p.parseSelectItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if !p.match(TokenComma) {
			break
		}
	}
	return items, nil
}

func (p *Parser) parseSelectItem() (interfaces.SelectItem, error) {
	// Check if wildcard *
	if p.current().Type == TokenStar {
		p.advance()
		return interfaces.SelectItem{
			Expr: interfaces.ColumnRef{Name: "*"},
		}, nil
	}

	expr, err := p.parseExpr()
	if err != nil {
		return interfaces.SelectItem{}, err
	}

	var alias string
	if p.match(TokenAs) {
		aliasTok, err := p.expectIdentOrType("AS alias")
		if err != nil {
			return interfaces.SelectItem{}, err
		}
		alias = aliasTok.Value
	} else if p.isAliasToken(p.current()) {
		aliasTok := p.advance()
		alias = aliasTok.Value
	}

	return interfaces.SelectItem{
		Expr:  expr,
		Alias: alias,
	}, nil
}

func (p *Parser) isAliasToken(tok Token) bool {
	switch tok.Type {
	case TokenIdent, TokenString:
		return true
	default:
		return false
	}
}

func (p *Parser) parseTableRef() (string, string, error) {
	tableTok, err := p.expectIdentOrType("table name")
	if err != nil {
		return "", "", err
	}
	tableName := tableTok.Value

	var alias string
	if p.match(TokenAs) {
		aliasTok, err := p.expectIdentOrType("table alias")
		if err != nil {
			return "", "", err
		}
		alias = aliasTok.Value
	} else if p.isAliasToken(p.current()) && p.current().Type != TokenJoin && p.current().Type != TokenLeft && p.current().Type != TokenInner && p.current().Type != TokenWhere && p.current().Type != TokenGroup && p.current().Type != TokenOrder && p.current().Type != TokenLimit && p.current().Type != TokenOffset {
		aliasTok := p.advance()
		alias = aliasTok.Value
	}

	return tableName, alias, nil
}

func (p *Parser) parseJoins() ([]interfaces.JoinClause, error) {
	var joins []interfaces.JoinClause
	for {
		joinType := interfaces.JoinInner
		if p.match(TokenInner) {
			if _, err := p.expect(TokenJoin, "INNER JOIN"); err != nil {
				return nil, err
			}
		} else if p.match(TokenLeft) {
			joinType = interfaces.JoinLeft
			// Optional OUTER
			_ = p.match(TokenIdent) // tolerate OUTER if present
			if _, err := p.expect(TokenJoin, "LEFT JOIN"); err != nil {
				return nil, err
			}
		} else if p.match(TokenJoin) {
			joinType = interfaces.JoinInner
		} else {
			break
		}

		table, alias, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}

		if _, err := p.expect(TokenOn, "JOIN condition"); err != nil {
			return nil, err
		}

		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		joins = append(joins, interfaces.JoinClause{
			Type:      joinType,
			Table:     table,
			Alias:     alias,
			Condition: cond,
		})
	}
	return joins, nil
}

func (p *Parser) parseExprList() ([]interfaces.Expr, error) {
	var exprs []interfaces.Expr
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
		if !p.match(TokenComma) {
			break
		}
	}
	return exprs, nil
}

func (p *Parser) parseOrderByList() ([]interfaces.OrderByItem, error) {
	var items []interfaces.OrderByItem
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		ascending := true
		if p.match(TokenAsc) {
			ascending = true
		} else if p.match(TokenDesc) {
			ascending = false
		}

		// Default NULLS ordering in SQL: NULLS LAST for ASC, NULLS FIRST for DESC.
		nullsFirst := !ascending
		if p.match(TokenNulls) {
			if p.match(TokenFirst) {
				nullsFirst = true
			} else if p.match(TokenLast) {
				nullsFirst = false
			} else {
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrSyntaxError,
					Message: "expected FIRST or LAST after NULLS",
				}
			}
		}

		items = append(items, interfaces.OrderByItem{
			Expr:       expr,
			Ascending:  ascending,
			NullsFirst: nullsFirst,
		})

		if !p.match(TokenComma) {
			break
		}
	}
	return items, nil
}

// Expression Parsing with Operator Precedence

func (p *Parser) parseExpr() (interfaces.Expr, error) {
	if err := p.enterDepth(); err != nil {
		return nil, err
	}
	defer p.leaveDepth()
	return p.parseOr()
}

func (p *Parser) parseOr() (interfaces.Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.match(TokenOr) {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = interfaces.BinaryExpr{
			Op:    interfaces.OpOr,
			Left:  left,
			Right: right,
		}
	}
	return left, nil
}

func (p *Parser) parseAnd() (interfaces.Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.match(TokenAnd) {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = interfaces.BinaryExpr{
			Op:    interfaces.OpAnd,
			Left:  left,
			Right: right,
		}
	}
	return left, nil
}

func (p *Parser) parseNot() (interfaces.Expr, error) {
	if p.match(TokenNot) {
		op, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return interfaces.UnaryExpr{
			Op:      "NOT",
			Operand: op,
		}, nil
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() (interfaces.Expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}

	for {
		if p.match(TokenEq) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpEq, Left: left, Right: right}
		} else if p.match(TokenNotEq) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpNotEq, Left: left, Right: right}
		} else if p.match(TokenLt) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpLt, Left: left, Right: right}
		} else if p.match(TokenLtEq) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpLtEq, Left: left, Right: right}
		} else if p.match(TokenGt) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpGt, Left: left, Right: right}
		} else if p.match(TokenGtEq) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpGtEq, Left: left, Right: right}
		} else if p.match(TokenLike) {
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpLike, Left: left, Right: right}
		} else if p.match(TokenIs) {
			if p.match(TokenNot) {
				if _, err := p.expect(TokenNull, "IS NOT NULL"); err != nil {
					return nil, err
				}
				left = interfaces.UnaryExpr{Op: "IS NOT NULL", Operand: left}
			} else {
				if _, err := p.expect(TokenNull, "IS NULL"); err != nil {
					return nil, err
				}
				left = interfaces.UnaryExpr{Op: "IS NULL", Operand: left}
			}
		} else {
			break
		}
	}
	return left, nil
}

func (p *Parser) parseAdditive() (interfaces.Expr, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}

	for {
		if p.match(TokenPlus) {
			right, err := p.parseMultiplicative()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpAdd, Left: left, Right: right}
		} else if p.match(TokenMinus) {
			right, err := p.parseMultiplicative()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpSub, Left: left, Right: right}
		} else {
			break
		}
	}
	return left, nil
}

func (p *Parser) parseMultiplicative() (interfaces.Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for {
		if p.match(TokenStar) {
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpMul, Left: left, Right: right}
		} else if p.match(TokenSlash) {
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			left = interfaces.BinaryExpr{Op: interfaces.OpDiv, Left: left, Right: right}
		} else {
			break
		}
	}
	return left, nil
}

func (p *Parser) parseUnary() (interfaces.Expr, error) {
	if p.match(TokenPlus) {
		return p.parseUnary()
	}
	if p.match(TokenMinus) {
		op, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		// If operand is literal number, negate it directly
		if lit, ok := op.(interfaces.LiteralExpr); ok {
			if lit.Type == interfaces.TypeInt64 {
				lit.Value = -lit.Value.(int64)
				return lit, nil
			}
			if lit.Type == interfaces.TypeFloat64 {
				lit.Value = -lit.Value.(float64)
				return lit, nil
			}
		}
		return interfaces.UnaryExpr{Op: "-", Operand: op}, nil
	}
	return p.parsePrimary()
}

func (p *Parser) parsePrimary() (interfaces.Expr, error) {
	cur := p.current()
	switch cur.Type {
	case TokenLeftParen:
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenRightParen, "parenthesized expression"); err != nil {
			return nil, err
		}
		return expr, nil

	case TokenNull:
		p.advance()
		return interfaces.LiteralExpr{IsNull: true}, nil

	case TokenTrue:
		p.advance()
		return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: true}, nil

	case TokenFalse:
		p.advance()
		return interfaces.LiteralExpr{Type: interfaces.TypeBool, Value: false}, nil

	case TokenNumber:
		p.advance()
		if strings.Contains(cur.Value, ".") {
			f, err := strconv.ParseFloat(cur.Value, 64)
			if err != nil {
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrSyntaxError,
					Message: fmt.Sprintf("invalid float literal %s", cur.Value),
				}
			}
			return interfaces.LiteralExpr{Type: interfaces.TypeFloat64, Value: f}, nil
		}
		i, err := strconv.ParseInt(cur.Value, 10, 64)
		if err != nil {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrSyntaxError,
				Message: fmt.Sprintf("invalid int literal %s", cur.Value),
			}
		}
		return interfaces.LiteralExpr{Type: interfaces.TypeInt64, Value: i}, nil

	case TokenString:
		p.advance()
		return interfaces.LiteralExpr{Type: interfaces.TypeVarChar, Value: cur.Value}, nil

	case TokenCase:
		return p.parseCaseExpr()

	case TokenCast:
		return p.parseCastExpr()

	case TokenIdent:
		name := cur.Value
		upper := strings.ToUpper(name)

		// Check for aggregate functions
		if upper == "COUNT" || upper == "SUM" || upper == "AVG" || upper == "MIN" || upper == "MAX" {
			if p.peekType(1) == TokenLeftParen {
				p.advance() // consume name
				return p.parseAggregateCall(upper)
			}
		}

		p.advance() // consume identifier

		// Check if qualified column ref (table.col)
		if p.match(TokenDot) {
			colTok, err := p.expectIdentOrStar("qualified column name")
			if err != nil {
				return nil, err
			}
			return interfaces.ColumnRef{Table: name, Name: colTok.Value}, nil
		}

		return interfaces.ColumnRef{Name: name}, nil

	default:
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("unexpected token %s at pos %d", cur.Value, cur.Pos),
		}
	}
}

func (p *Parser) parseCaseExpr() (interfaces.Expr, error) {
	p.advance() // consume CASE

	var operand interfaces.Expr
	if p.current().Type != TokenWhen {
		op, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		operand = op
	}

	var whens []interfaces.CaseWhen
	for p.match(TokenWhen) {
		whenCond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenThen, "WHEN ... THEN"); err != nil {
			return nil, err
		}
		thenExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		whens = append(whens, interfaces.CaseWhen{
			When: whenCond,
			Then: thenExpr,
		})
	}

	if len(whens) == 0 {
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: "CASE must have at least one WHEN branch",
		}
	}

	var elseExpr interfaces.Expr
	if p.match(TokenElse) {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elseExpr = e
	}

	if _, err := p.expect(TokenEnd, "CASE ... END"); err != nil {
		return nil, err
	}

	return interfaces.CaseExpr{
		Operand: operand,
		Whens:   whens,
		Else:    elseExpr,
	}, nil
}

func (p *Parser) parseCastExpr() (interfaces.Expr, error) {
	p.advance() // consume CAST
	if _, err := p.expect(TokenLeftParen, "CAST("); err != nil {
		return nil, err
	}

	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(TokenAs, "CAST(... AS"); err != nil {
		return nil, err
	}

	targetType, err := p.parseDataType()
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(TokenRightParen, "CAST(...)"); err != nil {
		return nil, err
	}

	return interfaces.CastExpr{
		Expr:       expr,
		TargetType: targetType,
	}, nil
}

func (p *Parser) parseDataType() (interfaces.DataType, error) {
	cur := p.current()
	switch cur.Type {
	case TokenInt64Type:
		p.advance()
		return interfaces.TypeInt64, nil
	case TokenFloat64Type:
		p.advance()
		return interfaces.TypeFloat64, nil
	case TokenDecimalType:
		p.advance()
		// Optional precision/scale like DECIMAL(10, 2)
		if p.match(TokenLeftParen) {
			_ = p.match(TokenNumber)
			if p.match(TokenComma) {
				_ = p.match(TokenNumber)
			}
			if _, err := p.expect(TokenRightParen, "DECIMAL precision"); err != nil {
				return 0, err
			}
		}
		return interfaces.TypeDecimal, nil
	case TokenVarCharType:
		p.advance()
		// Optional length like VARCHAR(255)
		if p.match(TokenLeftParen) {
			_ = p.match(TokenNumber)
			if _, err := p.expect(TokenRightParen, "VARCHAR length"); err != nil {
				return 0, err
			}
		}
		return interfaces.TypeVarChar, nil
	case TokenDateType:
		p.advance()
		return interfaces.TypeDate, nil
	case TokenBoolType:
		p.advance()
		return interfaces.TypeBool, nil
	default:
		return 0, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("unknown or unsupported data type %s", cur.Value),
		}
	}
}

func (p *Parser) parseAggregateCall(fnName string) (interfaces.Expr, error) {
	if _, err := p.expect(TokenLeftParen, "aggregate ("); err != nil {
		return nil, err
	}

	distinct := p.match(TokenDistinct)

	var args []interfaces.Expr
	if p.match(TokenStar) {
		// COUNT(*)
		args = append(args, interfaces.ColumnRef{Name: "*"})
	} else if p.current().Type != TokenRightParen {
		argList, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		args = argList
	}

	if _, err := p.expect(TokenRightParen, "aggregate )"); err != nil {
		return nil, err
	}

	return interfaces.AggregateCall{
		Func:     fnName,
		Distinct: distinct,
		Args:     args,
	}, nil
}

func (p *Parser) peekType(offset int) TokenType {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return TokenEOF
	}
	return p.tokens[idx].Type
}

func (p *Parser) expectIdentOrType(context string) (Token, error) {
	cur := p.current()
	switch cur.Type {
	case TokenIdent, TokenInt64Type, TokenFloat64Type, TokenDecimalType, TokenVarCharType, TokenDateType, TokenBoolType:
		return p.advance(), nil
	default:
		return Token{}, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("expected identifier for %s at pos %d, got %s", context, cur.Pos, cur.Value),
		}
	}
}

func (p *Parser) expectIdentOrStar(context string) (Token, error) {
	cur := p.current()
	if cur.Type == TokenIdent || cur.Type == TokenStar {
		return p.advance(), nil
	}
	return Token{}, &interfaces.EngineError{
		Code:    interfaces.ErrSyntaxError,
		Message: fmt.Sprintf("expected identifier or * for %s at pos %d, got %s", context, cur.Pos, cur.Value),
	}
}
