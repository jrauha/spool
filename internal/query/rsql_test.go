package query

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRSQL(t *testing.T) {
	expression, err := Parse(`text=search='postgres replication';(read==false,feed.id=in=(feed-1,feed-2));date=ge=2025-01-01T00:00:00Z`)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(expression.Conjunctions) != 1 || len(expression.Conjunctions[0].Terms) != 3 {
		t.Fatalf("expression = %#v", expression)
	}

	search := expression.Conjunctions[0].Terms[0].Comparison
	if search.Selector != "text" || search.Operator != OperatorSearch || search.Values()[0] != "postgres replication" {
		t.Fatalf("search comparison = %#v", search)
	}
	nested := expression.Conjunctions[0].Terms[1].Nested
	if nested == nil || len(nested.Conjunctions) != 2 {
		t.Fatalf("nested expression = %#v", nested)
	}
	list := nested.Conjunctions[1].Terms[0].Comparison
	if len(list.Values()) != 2 || list.Values()[1] != "feed-2" {
		t.Fatalf("list comparison = %#v", list)
	}
	date := expression.Conjunctions[0].Terms[2].Comparison
	if date.Operator != OperatorGreaterEqual {
		t.Fatalf("date operator = %q, want %q", date.Operator, OperatorGreaterEqual)
	}
}

func TestParseRSQLPrecedence(t *testing.T) {
	expression, err := Parse(`title==one,title==two;read==false`)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(expression.Conjunctions) != 2 {
		t.Fatalf("OR branches = %d, want 2", len(expression.Conjunctions))
	}
	if len(expression.Conjunctions[0].Terms) != 1 || len(expression.Conjunctions[1].Terms) != 2 {
		t.Fatalf("expression = %#v", expression)
	}
}

func TestParseRSQLQuotedAndEscapedValues(t *testing.T) {
	expression, err := Parse(`title=='reader\'s digest';url==https://example.com/a\,b`)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	terms := expression.Conjunctions[0].Terms
	if got := terms[0].Comparison.Values()[0]; got != "reader's digest" {
		t.Fatalf("quoted value = %q", got)
	}
	if got := terms[1].Comparison.Values()[0]; got != "https://example.com/a,b" {
		t.Fatalf("escaped value = %q", got)
	}
}

func TestParseRSQLRejectsInvalidFilters(t *testing.T) {
	tests := []string{
		`title=unknown=value`,
		`feed.id=in=one`,
		`title==(one,two)`,
		strings.Repeat("(", maxParserDepth+1) + "title==one" + strings.Repeat(")", maxParserDepth+1),
		strings.Repeat("a", maxFilterBytes+1),
	}
	for _, filter := range tests {
		if _, err := Parse(filter); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalid", filter, err)
		}
	}
}

func TestParseEmptyRSQL(t *testing.T) {
	expression, err := Parse("  ")
	if err != nil || expression != nil {
		t.Fatalf("Parse empty = (%#v, %v), want nil", expression, err)
	}
}

func FuzzParseRSQL(f *testing.F) {
	for _, filter := range []string{"title==spool", "read==false", `(title==one,title==two);date>2025-01-01T00:00:00Z`} {
		f.Add(filter)
	}
	f.Fuzz(func(t *testing.T, filter string) {
		_, _ = Parse(filter)
	})
}
