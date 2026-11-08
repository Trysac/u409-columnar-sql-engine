package parser_test

import (
	"testing"

	"github.com/Trysac/u409-columnar-sql-engine/internal/parser"
)

func TestLexerBasic(t *testing.T) {
	sql := "SELECT a, b FROM tbl WHERE a = 10 AND b != 'text' -- comment\n/* block */ LIMIT 5"
	l := parser.NewLexer(sql)

	expected := []parser.TokenType{
		parser.TokenSelect,
		parser.TokenIdent,
		parser.TokenComma,
		parser.TokenIdent,
		parser.TokenFrom,
		parser.TokenIdent,
		parser.TokenWhere,
		parser.TokenIdent,
		parser.TokenEq,
		parser.TokenNumber,
		parser.TokenAnd,
		parser.TokenIdent,
		parser.TokenNotEq,
		parser.TokenString,
		parser.TokenLimit,
		parser.TokenNumber,
		parser.TokenEOF,
	}

	for idx, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp {
			t.Fatalf("[%d] expected token type %v, got %v (%s)", idx, exp, tok.Type, tok.Value)
		}
	}
}

func TestLexerOperatorsAndIdentifiers(t *testing.T) {
	sql := `<> <= >= < > + - * / ; . ( ) ` + "`quoted`" + ` "col" 'don''t'`
	l := parser.NewLexer(sql)

	toks := []parser.TokenType{
		parser.TokenNotEq,
		parser.TokenLtEq,
		parser.TokenGtEq,
		parser.TokenLt,
		parser.TokenGt,
		parser.TokenPlus,
		parser.TokenMinus,
		parser.TokenStar,
		parser.TokenSlash,
		parser.TokenSemicolon,
		parser.TokenDot,
		parser.TokenLeftParen,
		parser.TokenRightParen,
		parser.TokenIdent,
		parser.TokenIdent,
		parser.TokenString,
		parser.TokenEOF,
	}

	for idx, exp := range toks {
		tok := l.NextToken()
		if tok.Type != exp {
			t.Fatalf("[%d] expected %v, got %v (%s)", idx, exp, tok.Type, tok.Value)
		}
	}
}

func TestLexerErrors(t *testing.T) {
	// Unterminated string
	l1 := parser.NewLexer("'unterminated")
	tok1 := l1.NextToken()
	if tok1.Type != parser.TokenError {
		t.Fatalf("expected error for unterminated string, got %v", tok1.Type)
	}

	// Unterminated identifier
	l2 := parser.NewLexer("`unterminated")
	tok2 := l2.NextToken()
	if tok2.Type != parser.TokenError {
		t.Fatalf("expected error for unterminated ident, got %v", tok2.Type)
	}

	// Unknown char
	l3 := parser.NewLexer("!")
	tok3 := l3.NextToken()
	if tok3.Type != parser.TokenError {
		t.Fatalf("expected error for lone '!', got %v", tok3.Type)
	}
}
