# Recursive descent SQL parser with operator precedence Pratt-style layering

Status: accepted (generated, pending human review)

## Context

The columnar SQL engine requires a zero-external-dependency, deterministic SQL lexer and recursive descent parser that constructs AST structures conforming to interfaces/parser.go while strictly enforcing query size limits and recursion depth limits.

## Decision

Implement internal/parser featuring a handwritten lexer for SQL tokens and a recursive descent parser with precedence climbing for binary/unary expressions, aggregates, joins, orderings, and projections, returning typed interfaces.EngineError on syntax, depth, or size violations.

## Consequences

Provides full AST parsing support conforming to the frozen interfaces without external dependencies, with clear error diagnostics and protective recursion depth enforcement.
