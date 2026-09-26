package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	itemquery "github.com/spool-reader/spool/internal/query"
)

func TestCompileItemFilter(t *testing.T) {
	expression, err := itemquery.Parse(`text=search='postgres replication';(read==false,feed.id=in=(11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222));date>=2025-01-01T00:00:00Z`)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &itemFilterCompiler{args: []any{"user-1"}}
	compiled, err := compiler.compileExpression(expression)
	if err != nil {
		t.Fatalf("compileExpression returned error: %v", err)
	}
	for _, fragment := range []string{
		"search_vector @@ websearch_to_tsquery('simple', $2)",
		"read_at IS NULL",
		"feed_id IN ($3, $4)",
		"sort_at >= $5",
	} {
		if !strings.Contains(compiled, fragment) {
			t.Fatalf("compiled filter %q does not contain %q", compiled, fragment)
		}
	}
	if len(compiler.args) != 5 || compiler.args[1] != "postgres replication" {
		t.Fatalf("args = %#v", compiler.args)
	}
	if len(compiler.searchRanks) != 1 {
		t.Fatalf("search ranks = %#v", compiler.searchRanks)
	}
}

func TestCompileItemFilterParameterizesValues(t *testing.T) {
	expression, err := itemquery.Parse(`title=='x\' OR true; --'`)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &itemFilterCompiler{args: []any{"user-1"}}
	compiled, err := compiler.compileExpression(expression)
	if err != nil {
		t.Fatalf("compileExpression returned error: %v", err)
	}
	if strings.Contains(compiled, "OR true") || !strings.Contains(compiled, "title = $2") {
		t.Fatalf("compiled filter = %q", compiled)
	}
	if got := compiler.args[1]; got != "x' OR true; --" {
		t.Fatalf("bound value = %q", got)
	}
}

func TestCompileItemFilterRejectsUnknownSelector(t *testing.T) {
	expression, err := itemquery.Parse(`passwordHash==secret`)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &itemFilterCompiler{args: []any{"user-1"}}
	_, err = compiler.compileExpression(expression)
	if !errors.Is(err, itemquery.ErrInvalid) {
		t.Fatalf("compileExpression error = %v, want ErrInvalid", err)
	}
}

func TestCompileWildcardEscapesSQLPatternCharacters(t *testing.T) {
	if got, want := wildcardPattern(`100%_*`), `100\%\_%`; got != want {
		t.Fatalf("wildcardPattern = %q, want %q", got, want)
	}
}

func TestItemPaginationBeforeReversesOrdering(t *testing.T) {
	cursor := &ItemQueryCursor{
		SortAt: time.Now().UTC(),
		ItemID: "11111111-1111-1111-1111-111111111111",
		Rank:   0.5,
		Before: true,
	}
	for _, test := range []struct {
		sort        ItemQuerySort
		searchRanks []string
		predicate   string
		order       string
	}{
		{
			sort:      ItemQuerySortNewest,
			predicate: "(sort_at, id) > ($2, $3)",
			order:     "sort_at ASC, id ASC",
		},
		{
			sort:      ItemQuerySortOldest,
			predicate: "(sort_at, id) < ($2, $3)",
			order:     "sort_at DESC, id DESC",
		},
		{
			sort:        ItemQuerySortRelevance,
			searchRanks: []string{"search_rank"},
			predicate:   "(search_rank > $2 OR (search_rank = $2 AND (sort_at, id) > ($3, $4)))",
			order:       "search_rank ASC, sort_at ASC, id ASC",
		},
	} {
		compiler := &itemFilterCompiler{args: []any{"user-1"}, searchRanks: test.searchRanks}
		predicate, order, err := compiler.pagination(ItemQuery{Sort: test.sort, Cursor: cursor})
		if err != nil {
			t.Fatalf("pagination(%q) returned error: %v", test.sort, err)
		}
		if predicate != test.predicate || order != test.order {
			t.Fatalf("pagination(%q) = %q, %q; want %q, %q", test.sort, predicate, order, test.predicate, test.order)
		}
	}
}
