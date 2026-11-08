package parser

import (
	"strings"
	"unicode"
)

// Lexer tokenizes SQL source text.
type Lexer struct {
	input []rune
	pos   int
}

// NewLexer creates a new Lexer for the given SQL string.
func NewLexer(sql string) *Lexer {
	return &Lexer{
		input: []rune(sql),
		pos:   0,
	}
}

var keywords = map[string]TokenType{
	"SELECT":   TokenSelect,
	"FROM":     TokenFrom,
	"WHERE":    TokenWhere,
	"GROUP":    TokenGroup,
	"BY":       TokenBy,
	"HAVING":   TokenHaving,
	"ORDER":    TokenOrder,
	"ASC":      TokenAsc,
	"DESC":     TokenDesc,
	"NULLS":    TokenNulls,
	"FIRST":    TokenFirst,
	"LAST":     TokenLast,
	"LIMIT":    TokenLimit,
	"OFFSET":   TokenOffset,
	"JOIN":     TokenJoin,
	"INNER":    TokenInner,
	"LEFT":     TokenLeft,
	"RIGHT":    TokenRight,
	"FULL":     TokenFull,
	"CROSS":    TokenCross,
	"ON":       TokenOn,
	"AS":       TokenAs,
	"AND":      TokenAnd,
	"OR":       TokenOr,
	"NOT":      TokenNot,
	"IS":       TokenIs,
	"NULL":     TokenNull,
	"TRUE":     TokenTrue,
	"FALSE":    TokenFalse,
	"LIKE":     TokenLike,
	"CASE":     TokenCase,
	"WHEN":     TokenWhen,
	"THEN":     TokenThen,
	"ELSE":     TokenElse,
	"END":      TokenEnd,
	"CAST":     TokenCast,
	"DISTINCT": TokenDistinct,
	"INT64":    TokenInt64Type,
	"INT":      TokenInt64Type,
	"INTEGER":  TokenInt64Type,
	"BIGINT":   TokenInt64Type,
	"FLOAT64":  TokenFloat64Type,
	"FLOAT":    TokenFloat64Type,
	"DOUBLE":   TokenFloat64Type,
	"DECIMAL":  TokenDecimalType,
	"VARCHAR":  TokenVarCharType,
	"STRING":   TokenVarCharType,
	"TEXT":     TokenVarCharType,
	"DATE":     TokenDateType,
	"BOOL":     TokenBoolType,
	"BOOLEAN":  TokenBoolType,
}

// NextToken yields the next token from input.
func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()
	if l.pos >= len(l.input) {
		return Token{Type: TokenEOF, Value: "", Pos: l.pos}
	}

	startPos := l.pos
	ch := l.input[l.pos]

	switch ch {
	case ',':
		l.pos++
		return Token{Type: TokenComma, Value: ",", Pos: startPos}
	case '.':
		l.pos++
		return Token{Type: TokenDot, Value: ".", Pos: startPos}
	case ';':
		l.pos++
		return Token{Type: TokenSemicolon, Value: ";", Pos: startPos}
	case '(':
		l.pos++
		return Token{Type: TokenLeftParen, Value: "(", Pos: startPos}
	case ')':
		l.pos++
		return Token{Type: TokenRightParen, Value: ")", Pos: startPos}
	case '+':
		l.pos++
		return Token{Type: TokenPlus, Value: "+", Pos: startPos}
	case '-':
		l.pos++
		return Token{Type: TokenMinus, Value: "-", Pos: startPos}
	case '*':
		l.pos++
		return Token{Type: TokenStar, Value: "*", Pos: startPos}
	case '/':
		l.pos++
		return Token{Type: TokenSlash, Value: "/", Pos: startPos}
	case '=':
		l.pos++
		return Token{Type: TokenEq, Value: "=", Pos: startPos}
	case '!':
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
			l.pos += 2
			return Token{Type: TokenNotEq, Value: "!=", Pos: startPos}
		}
		l.pos++
		return Token{Type: TokenError, Value: "!", Pos: startPos}
	case '<':
		if l.pos+1 < len(l.input) {
			if l.input[l.pos+1] == '=' {
				l.pos += 2
				return Token{Type: TokenLtEq, Value: "<=", Pos: startPos}
			}
			if l.input[l.pos+1] == '>' {
				l.pos += 2
				return Token{Type: TokenNotEq, Value: "<>", Pos: startPos}
			}
		}
		l.pos++
		return Token{Type: TokenLt, Value: "<", Pos: startPos}
	case '>':
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
			l.pos += 2
			return Token{Type: TokenGtEq, Value: ">=", Pos: startPos}
		}
		l.pos++
		return Token{Type: TokenGt, Value: ">", Pos: startPos}
	case '"':
		return l.readQuotedIdent('"', startPos)
	case '`':
		return l.readQuotedIdent('`', startPos)
	default:
		if ch == '\'' {
			return l.readString(startPos)
		}
		if unicode.IsDigit(ch) {
			return l.readNumber(startPos)
		}
		if unicode.IsLetter(ch) || ch == '_' {
			return l.readIdentOrKeyword(startPos)
		}
		l.pos++
		return Token{Type: TokenError, Value: string(ch), Pos: startPos}
	}
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if unicode.IsSpace(ch) {
			l.pos++
			continue
		}
		// Single line comment --
		if ch == '-' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '-' {
			l.pos += 2
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		// Multi line comment /* ... */
		if ch == '/' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '*' {
			l.pos += 2
			for l.pos+1 < len(l.input) && !(l.input[l.pos] == '*' && l.input[l.pos+1] == '/') {
				l.pos++
			}
			if l.pos+1 < len(l.input) {
				l.pos += 2
			} else {
				l.pos = len(l.input)
			}
			continue
		}
		break
	}
}

func (l *Lexer) readString(startPos int) Token {
	l.pos++ // skip initial '
	var b strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\'' {
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
				b.WriteRune('\'')
				l.pos += 2
				continue
			}
			l.pos++
			return Token{Type: TokenString, Value: b.String(), Pos: startPos}
		}
		b.WriteRune(ch)
		l.pos++
	}
	return Token{Type: TokenError, Value: "unterminated string literal", Pos: startPos}
}

func (l *Lexer) readQuotedIdent(quote rune, startPos int) Token {
	l.pos++ // skip leading quote
	var b strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == quote {
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == quote {
				b.WriteRune(quote)
				l.pos += 2
				continue
			}
			l.pos++
			return Token{Type: TokenIdent, Value: b.String(), Pos: startPos}
		}
		b.WriteRune(ch)
		l.pos++
	}
	return Token{Type: TokenError, Value: "unterminated quoted identifier", Pos: startPos}
}

func (l *Lexer) readNumber(startPos int) Token {
	hasDot := false
	var b strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if unicode.IsDigit(ch) {
			b.WriteRune(ch)
			l.pos++
		} else if ch == '.' && !hasDot && l.pos+1 < len(l.input) && unicode.IsDigit(l.input[l.pos+1]) {
			hasDot = true
			b.WriteRune('.')
			l.pos++
		} else {
			break
		}
	}
	return Token{Type: TokenNumber, Value: b.String(), Pos: startPos}
}

func (l *Lexer) readIdentOrKeyword(startPos int) Token {
	var b strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
			b.WriteRune(ch)
			l.pos++
		} else {
			break
		}
	}
	val := b.String()
	upper := strings.ToUpper(val)
	if tokType, ok := keywords[upper]; ok {
		return Token{Type: tokType, Value: val, Pos: startPos}
	}
	return Token{Type: TokenIdent, Value: val, Pos: startPos}
}
