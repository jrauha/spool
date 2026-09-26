package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

const (
	MaxSearchTextBytes = 512

	maxFilterBytes = 4 << 10
	maxFilterDepth = 8
	maxParserDepth = maxFilterDepth + 1
	maxComparisons = 50
	maxListValues  = 50
)

var ErrInvalid = errors.New("invalid query")

var rsqlLexer = lexer.MustSimple([]lexer.SimpleRule{
	{Name: "Whitespace", Pattern: `\s+`},
	{Name: "Operator", Pattern: `=search=|=out=|=in=|=gt=|=ge=|=lt=|=le=|>=|<=|==|!=|>|<`},
	{Name: "String", Pattern: `'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"`},
	{Name: "Atom", Pattern: `(?:\\.|[^\s(),;=!<>'"])+`},
	{Name: "Punctuation", Pattern: `[(),;]`},
})

var rsqlParser = participle.MustBuild[Expression](
	participle.Lexer(rsqlLexer),
	participle.Elide("Whitespace"),
	participle.UseLookahead(2),
)

type Expression struct {
	Conjunctions []*Conjunction `parser:"@@ (',' @@)*"`
}

type Conjunction struct {
	Terms []*Term `parser:"@@ (';' @@)*"`
}

type Term struct {
	Comparison *Comparison `parser:"  @@"`
	Nested     *Expression `parser:"| '(' @@ ')'"`
}

type Comparison struct {
	Selector string   `parser:"@Atom"`
	Operator Operator `parser:"@Operator"`
	Scalar   *string  `parser:"( @(String | Atom)"`
	List     []string `parser:"| '(' @(String | Atom) (',' @(String | Atom))* ')' )"`
}

type Operator string

const (
	OperatorEqual        Operator = "=="
	OperatorNotEqual     Operator = "!="
	OperatorGreater      Operator = ">"
	OperatorGreaterEqual Operator = ">="
	OperatorLess         Operator = "<"
	OperatorLessEqual    Operator = "<="
	OperatorIn           Operator = "=in="
	OperatorOut          Operator = "=out="
	OperatorSearch       Operator = "=search="
)

func Parse(raw string) (*Expression, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > maxFilterBytes {
		return nil, invalidf("filter exceeds %d bytes", maxFilterBytes)
	}
	if err := validateParserDepth(raw); err != nil {
		return nil, err
	}

	expression, err := rsqlParser.ParseString("filter", raw)
	if err != nil {
		return nil, invalidf("%v", err)
	}
	if err := normalizeAndValidate(expression, 1, new(int)); err != nil {
		return nil, err
	}
	return expression, nil
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func validateParserDepth(raw string) error {
	depth := 0
	var quote byte
	escaped := false
	for index := 0; index < len(raw); index++ {
		current := raw[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '(':
			depth++
			if depth > maxParserDepth {
				return invalidf("filter nesting exceeds %d levels", maxParserDepth)
			}
		case ')':
			depth--
			if depth < 0 {
				return invalidf("unmatched closing parenthesis")
			}
		}
	}
	return nil
}

func normalizeAndValidate(expression *Expression, depth int, comparisons *int) error {
	if expression == nil || len(expression.Conjunctions) == 0 {
		return invalidf("empty expression")
	}
	if depth > maxFilterDepth {
		return invalidf("filter nesting exceeds %d levels", maxFilterDepth)
	}
	for _, conjunction := range expression.Conjunctions {
		if conjunction == nil || len(conjunction.Terms) == 0 {
			return invalidf("empty conjunction")
		}
		for _, term := range conjunction.Terms {
			switch {
			case term == nil:
				return invalidf("empty term")
			case term.Comparison != nil:
				*comparisons = *comparisons + 1
				if *comparisons > maxComparisons {
					return invalidf("filter exceeds %d comparisons", maxComparisons)
				}
				if err := normalizeComparison(term.Comparison); err != nil {
					return err
				}
			case term.Nested != nil:
				if err := normalizeAndValidate(term.Nested, depth+1, comparisons); err != nil {
					return err
				}
			default:
				return invalidf("empty term")
			}
		}
	}
	return nil
}

func normalizeComparison(comparison *Comparison) error {
	comparison.Operator = canonicalOperator(comparison.Operator)
	isList := comparison.Operator == OperatorIn || comparison.Operator == OperatorOut
	if isList && len(comparison.List) == 0 {
		return invalidf("operator %q requires a value list", comparison.Operator)
	}
	if !isList && comparison.Scalar == nil {
		return invalidf("operator %q requires one value", comparison.Operator)
	}
	if len(comparison.List) > maxListValues {
		return invalidf("value list exceeds %d entries", maxListValues)
	}
	if comparison.Scalar != nil {
		value, err := decodeValue(*comparison.Scalar)
		if err != nil {
			return err
		}
		comparison.Scalar = &value
	}
	for index, raw := range comparison.List {
		value, err := decodeValue(raw)
		if err != nil {
			return err
		}
		comparison.List[index] = value
	}
	return nil
}

func canonicalOperator(operator Operator) Operator {
	switch operator {
	case "=gt=":
		return OperatorGreater
	case "=ge=":
		return OperatorGreaterEqual
	case "=lt=":
		return OperatorLess
	case "=le=":
		return OperatorLessEqual
	default:
		return operator
	}
}

func decodeValue(raw string) (string, error) {
	if len(raw) < 2 || (raw[0] != '\'' && raw[0] != '"') {
		return unescapeBare(raw), nil
	}
	if raw[len(raw)-1] != raw[0] {
		return "", invalidf("unterminated quoted value")
	}
	if raw[0] == '"' {
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", invalidf("invalid quoted value")
		}
		return value, nil
	}

	var value strings.Builder
	for index := 1; index < len(raw)-1; index++ {
		if raw[index] == '\\' && index+1 < len(raw)-1 {
			index++
		}
		value.WriteByte(raw[index])
	}
	return value.String(), nil
}

func unescapeBare(raw string) string {
	var value strings.Builder
	for index := 0; index < len(raw); index++ {
		if raw[index] == '\\' && index+1 < len(raw) {
			index++
		}
		value.WriteByte(raw[index])
	}
	return value.String()
}

func (c *Comparison) Values() []string {
	if c == nil {
		return nil
	}
	if c.Scalar != nil {
		return []string{*c.Scalar}
	}
	return c.List
}

func (e *Expression) HasComparison(selector string, operator Operator) bool {
	if e == nil {
		return false
	}
	for _, conjunction := range e.Conjunctions {
		for _, term := range conjunction.Terms {
			if term.Comparison != nil && term.Comparison.Selector == selector && term.Comparison.Operator == operator {
				return true
			}
			if term.Nested != nil && term.Nested.HasComparison(selector, operator) {
				return true
			}
		}
	}
	return false
}
