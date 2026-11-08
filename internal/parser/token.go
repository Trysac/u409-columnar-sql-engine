package parser

import "fmt"

// TokenType represents the lexical category of a SQL token.
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenError
	TokenIdent
	TokenString
	TokenNumber
	TokenComma
	TokenDot
	TokenSemicolon
	TokenLeftParen
	TokenRightParen
	TokenPlus
	TokenMinus
	TokenStar
	TokenSlash
	TokenEq
	TokenNotEq
	TokenLt
	TokenLtEq
	TokenGt
	TokenGtEq

	// Keywords
	TokenSelect
	TokenFrom
	TokenWhere
	TokenGroup
	TokenBy
	TokenHaving
	TokenOrder
	TokenAsc
	TokenDesc
	TokenNulls
	TokenFirst
	TokenLast
	TokenLimit
	TokenOffset
	TokenJoin
	TokenInner
	TokenLeft
	TokenRight
	TokenFull
	TokenCross
	TokenOn
	TokenAs
	TokenAnd
	TokenOr
	TokenNot
	TokenIs
	TokenNull
	TokenTrue
	TokenFalse
	TokenLike
	TokenCase
	TokenWhen
	TokenThen
	TokenElse
	TokenEnd
	TokenCast
	TokenDistinct

	// Types for CAST
	TokenInt64Type
	TokenFloat64Type
	TokenDecimalType
	TokenVarCharType
	TokenDateType
	TokenBoolType
)

// Token represents a scanned SQL lexical unit.
type Token struct {
	Type  TokenType
	Value string
	Pos   int
}

func (t Token) String() string {
	return fmt.Sprintf("Token(%d, %q, pos=%d)", t.Type, t.Value, t.Pos)
}
