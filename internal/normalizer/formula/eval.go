// Package formula implements a recursive descent evaluator for derived KPI formulas.
//
// The evaluator parses and evaluates in a single pass — no AST construction.
// Variable identifiers support dots (e.g. "registration.attempt_count") to match
// 3GPP-grounded KPI naming conventions used throughout Argus.
//
// Ternary expressions use deferred division-by-zero detection: during evaluation,
// division by zero produces math.NaN rather than an error. The ternary operator
// selects a branch, and the final Eval boundary checks for NaN/Inf — only erroring
// if the chosen result is non-finite. This allows "x > 0 ? a/x : 0" to work when x == 0.
package formula

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Formula grammar (complete — do not extend without a schema version bump):
//
//	expr     = ternary
//	ternary  = compare ('?' expr ':' expr)?
//	compare  = addition (('>' | '<' | '>=' | '<=' | '==' | '!=') addition)?
//	addition = term (('+' | '-') term)*
//	term     = factor (('*' | '/') factor)*
//	factor   = NUMBER | IDENTIFIER | '(' expr ')'
//	IDENTIFIER = [a-zA-Z_][a-zA-Z0-9_.]*  (KPI name, resolved from vars map)
//	NUMBER   = floating point literal
//
// Comparisons return 1.0 (true) or 0.0 (false).
// Ternary: condition != 0 selects true branch.
//
// No functions. No string operations. No external references.
// If a formula needs more than this, the KPI is wrong — break it into smaller derived KPIs.

// tokenType classifies lexer tokens.
type tokenType int

const (
	tokenNUMBER     tokenType = iota // floating point literal
	tokenIDENTIFIER                  // variable name (may contain dots)
	tokenPLUS                        // +
	tokenMINUS                       // -
	tokenSTAR                        // *
	tokenSLASH                       // /
	tokenLPAREN                      // (
	tokenRPAREN                      // )
	tokenQUESTION                    // ?
	tokenCOLON                       // :
	tokenGT                          // >
	tokenLT                          // <
	tokenGTE                         // >=
	tokenLTE                         // <=
	tokenEQ                          // ==
	tokenNEQ                         // !=
	tokenEOF                         // end of input
)

// token is a single lexer token with its type and literal value.
type token struct {
	typ tokenType
	val string
}

// lexer tokenizes a formula string.
type lexer struct {
	input string
	pos   int
}

// newLexer creates a lexer for the given input string.
func newLexer(input string) *lexer {
	return &lexer{input: input}
}

// next returns the next token from the input.
func (l *lexer) next() (token, error) {
	l.skipWhitespace()
	if l.pos >= len(l.input) {
		return token{typ: tokenEOF}, nil
	}

	ch := l.input[l.pos]

	switch ch {
	case '+':
		l.pos++
		return token{typ: tokenPLUS, val: "+"}, nil
	case '-':
		l.pos++
		return token{typ: tokenMINUS, val: "-"}, nil
	case '*':
		l.pos++
		return token{typ: tokenSTAR, val: "*"}, nil
	case '/':
		l.pos++
		return token{typ: tokenSLASH, val: "/"}, nil
	case '(':
		l.pos++
		return token{typ: tokenLPAREN, val: "("}, nil
	case ')':
		l.pos++
		return token{typ: tokenRPAREN, val: ")"}, nil
	case '?':
		l.pos++
		return token{typ: tokenQUESTION, val: "?"}, nil
	case ':':
		l.pos++
		return token{typ: tokenCOLON, val: ":"}, nil
	}

	if ch == '>' {
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
			l.pos += 2
			return token{typ: tokenGTE, val: ">="}, nil
		}
		l.pos++
		return token{typ: tokenGT, val: ">"}, nil
	}
	if ch == '<' {
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
			l.pos += 2
			return token{typ: tokenLTE, val: "<="}, nil
		}
		l.pos++
		return token{typ: tokenLT, val: "<"}, nil
	}
	if ch == '=' {
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
			l.pos += 2
			return token{typ: tokenEQ, val: "=="}, nil
		}
		return token{}, fmt.Errorf("unexpected character '=' at position %d (did you mean '=='?)", l.pos)
	}
	if ch == '!' {
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '=' {
			l.pos += 2
			return token{typ: tokenNEQ, val: "!="}, nil
		}
		return token{}, fmt.Errorf("unexpected character '!' at position %d (did you mean '!='?)", l.pos)
	}

	if ch >= '0' && ch <= '9' || (ch == '.' && l.pos+1 < len(l.input) && l.input[l.pos+1] >= '0' && l.input[l.pos+1] <= '9') {
		return l.readNumber()
	}

	if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
		return l.readIdentifier(), nil
	}

	return token{}, fmt.Errorf("unexpected character %q at position %d", ch, l.pos)
}

func (l *lexer) skipWhitespace() {
	for l.pos < len(l.input) && unicode.IsSpace(rune(l.input[l.pos])) {
		l.pos++
	}
}

func (l *lexer) readNumber() (token, error) {
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
		l.pos++
	}
	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		l.pos++
		for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			l.pos++
		}
	}
	if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
		l.pos++
		if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
			l.pos++
		}
		for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			l.pos++
		}
	}
	val := l.input[start:l.pos]
	if _, err := strconv.ParseFloat(val, 64); err != nil {
		return token{}, fmt.Errorf("invalid number %q at position %d", val, start)
	}
	return token{typ: tokenNUMBER, val: val}, nil
}

// readIdentifier consumes an identifier: [a-zA-Z_][a-zA-Z0-9_.]*
func (l *lexer) readIdentifier() token {
	start := l.pos
	l.pos++
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' {
			l.pos++
		} else {
			break
		}
	}
	return token{typ: tokenIDENTIFIER, val: l.input[start:l.pos]}
}

// parser is a recursive descent evaluator that tokenizes and evaluates in a single pass.
type parser struct {
	lex     *lexer
	current token
	vars    map[string]float64
}

func newParser(formula string, vars map[string]float64) (*parser, error) {
	p := &parser{
		lex:  newLexer(formula),
		vars: vars,
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *parser) advance() error {
	tok, err := p.lex.next()
	if err != nil {
		return err
	}
	p.current = tok
	return nil
}

func (p *parser) expect(typ tokenType) error {
	if p.current.typ != typ {
		return fmt.Errorf("expected %s but got %q", tokenName(typ), p.current.val)
	}
	return p.advance()
}

func tokenName(typ tokenType) string {
	switch typ {
	case tokenRPAREN:
		return "')'"
	case tokenCOLON:
		return "':'"
	case tokenEOF:
		return "end of expression"
	default:
		return fmt.Sprintf("token type %d", typ)
	}
}

// expr evaluates: expr = ternary
func (p *parser) expr() (float64, error) {
	return p.ternary()
}

// ternary evaluates: ternary = compare ('?' expr ':' expr)?
//
// Both branches are always parsed (the parser must consume all tokens). Division by zero
// during evaluation produces NaN instead of an error. The ternary selects the correct
// branch's value. Eval() checks the final result for NaN/Inf at the boundary.
func (p *parser) ternary() (float64, error) {
	cond, err := p.compare()
	if err != nil {
		return 0, err
	}
	if p.current.typ != tokenQUESTION {
		return cond, nil
	}
	if err := p.advance(); err != nil {
		return 0, err
	}

	trueBranch, trueErr := p.expr()
	if err := p.expect(tokenCOLON); err != nil {
		return 0, err
	}
	falseBranch, falseErr := p.expr()

	if cond != 0 {
		if trueErr != nil {
			return 0, trueErr
		}
		return trueBranch, nil
	}
	if falseErr != nil {
		return 0, falseErr
	}
	return falseBranch, nil
}

// compare evaluates: compare = addition (('>' | '<' | '>=' | '<=' | '==' | '!=') addition)?
func (p *parser) compare() (float64, error) {
	left, err := p.addition()
	if err != nil {
		return 0, err
	}

	op := p.current.typ
	switch op {
	case tokenGT, tokenLT, tokenGTE, tokenLTE, tokenEQ, tokenNEQ:
		if err := p.advance(); err != nil {
			return 0, err
		}
		right, err := p.addition()
		if err != nil {
			return 0, err
		}
		return evalComparison(op, left, right), nil
	}

	return left, nil
}

func evalComparison(op tokenType, left, right float64) float64 {
	var result bool
	switch op {
	case tokenGT:
		result = left > right
	case tokenLT:
		result = left < right
	case tokenGTE:
		result = left >= right
	case tokenLTE:
		result = left <= right
	case tokenEQ:
		result = left == right
	case tokenNEQ:
		result = left != right
	}
	if result {
		return 1.0
	}
	return 0.0
}

// addition evaluates: addition = term (('+' | '-') term)*
func (p *parser) addition() (float64, error) {
	result, err := p.term()
	if err != nil {
		return 0, err
	}
	for p.current.typ == tokenPLUS || p.current.typ == tokenMINUS {
		op := p.current.typ
		if err := p.advance(); err != nil {
			return 0, err
		}
		right, err := p.term()
		if err != nil {
			return 0, err
		}
		switch op {
		case tokenPLUS:
			result += right
		case tokenMINUS:
			result -= right
		}
	}
	return result, nil
}

// term evaluates: term = factor (('*' | '/') factor)*
// Division by zero produces NaN rather than an error — the ternary operator relies on this
// to discard non-finite results from the unselected branch.
func (p *parser) term() (float64, error) {
	result, err := p.factor()
	if err != nil {
		return 0, err
	}
	for p.current.typ == tokenSTAR || p.current.typ == tokenSLASH {
		op := p.current.typ
		if err := p.advance(); err != nil {
			return 0, err
		}
		right, err := p.factor()
		if err != nil {
			return 0, err
		}
		switch op {
		case tokenSTAR:
			result *= right
		case tokenSLASH:
			if right == 0 {
				result = math.NaN()
			} else {
				result /= right
			}
		}
	}
	return result, nil
}

// factor evaluates: factor = NUMBER | IDENTIFIER | '(' expr ')'
func (p *parser) factor() (float64, error) {
	switch p.current.typ {
	case tokenNUMBER:
		val, err := strconv.ParseFloat(p.current.val, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q: %w", p.current.val, err)
		}
		if err := p.advance(); err != nil {
			return 0, err
		}
		return val, nil

	case tokenIDENTIFIER:
		name := p.current.val
		val, ok := p.vars[name]
		if !ok {
			return 0, fmt.Errorf("unknown variable %q", name)
		}
		if err := p.advance(); err != nil {
			return 0, err
		}
		return val, nil

	case tokenLPAREN:
		if err := p.advance(); err != nil {
			return 0, err
		}
		val, err := p.expr()
		if err != nil {
			return 0, err
		}
		if err := p.expect(tokenRPAREN); err != nil {
			return 0, err
		}
		return val, nil

	case tokenEOF:
		return 0, fmt.Errorf("unexpected end of expression")

	default:
		return 0, fmt.Errorf("unexpected token %q", p.current.val)
	}
}

// Eval evaluates a formula string with the given variable bindings.
// Variables are KPI names mapping to their current values.
// Returns error on: parse failure, unknown variable, division by zero (when the
// non-finite result is actually selected by the expression).
func Eval(formula string, vars map[string]float64) (float64, error) {
	formula = strings.TrimSpace(formula)
	if formula == "" {
		return 0, fmt.Errorf("empty formula")
	}

	p, err := newParser(formula, vars)
	if err != nil {
		return 0, fmt.Errorf("parse error: %w", err)
	}

	result, err := p.expr()
	if err != nil {
		return 0, err
	}

	if p.current.typ != tokenEOF {
		return 0, fmt.Errorf("unexpected token %q after expression", p.current.val)
	}

	// Division by zero during evaluation produces NaN to allow ternary short-circuit.
	// If the final result is non-finite, the division by zero was in the selected path.
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, fmt.Errorf("division by zero")
	}

	return result, nil
}
