package application

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// PageSize is the fixed pagination window (D2): 10 per page, no env knob.
const PageSize = 10

// SearchService implements list and search use cases (ticket-search spec):
// composable AND filters, title/ID text search with D4 tokenization, stable
// pagination, and deterministic totals.
type SearchService struct {
	tickets TicketStore
	search  SearchStore
}

// NewSearchService wires list/search against the given ports.
func NewSearchService(tickets TicketStore, search SearchStore) *SearchService {
	return &SearchService{tickets: tickets, search: search}
}

// SearchResult is the list-view payload: the page's tickets, the total number
// of matches, and the requested page.
type SearchResult struct {
	Tickets []domain.Ticket
	Total   int
	Page    int
}

// Search applies the filters (AND), the D4-tokenized title/ID text filter,
// and pagination (page is 1-based; 0 behaves as 1).
func (s *SearchService) Search(ctx context.Context, q TicketQuery, page int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	q.Text, q.Numbers = BuildTitleQuery(q.Text)
	hasText := q.Text != "" || len(q.Numbers) > 0
	p := Page{Offset: (page - 1) * PageSize, Limit: PageSize}

	result := &SearchResult{Page: page}
	var err error
	if !hasText {
		result.Tickets, err = s.tickets.List(ctx, q, p)
	} else {
		result.Tickets, err = s.search.Search(ctx, q, p)
	}
	if err != nil {
		return nil, err
	}
	if !hasText {
		result.Total, err = s.tickets.Count(ctx, q)
	} else {
		result.Total, err = s.search.SearchCount(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// numberTokenRe finds every integer substring in the raw search text. The
// ticket ID search (TKT-N) is exact-number matching: "3" or "TKT-3" both
// resolve to number 3.
var numberTokenRe = regexp.MustCompile(`[0-9]+`)

// extractNumbers returns the distinct positive integers found in raw, in
// first-occurrence order. It is the ID-search side of the text filter.
func extractNumbers(raw string) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, m := range numberTokenRe.FindAllString(raw, -1) {
		n, err := strconv.ParseInt(m, 10, 64)
		if err != nil || n < 1 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// textTokens tokenizes raw user input for FTS5 (D4): each whitespace token
// is double-quoted with embedded quotes escaped. Quotes-only tokens carry no
// content and are dropped.
func textTokens(raw string) []string {
	tokens := strings.Fields(raw)
	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		escaped := strings.ReplaceAll(token, `"`, `""`)
		if strings.Trim(escaped, `"`) == "" {
			continue
		}
		quoted = append(quoted, `"`+escaped+`"`)
	}
	return quoted
}

// BuildTextQuery tokenizes raw user input for FTS5 (D4): each whitespace
// token is double-quoted with embedded quotes escaped, then joined with AND.
// Quotes-only tokens carry no content and are dropped; when nothing remains
// the query is empty, which means NO text filter — invalid input degrades to
// a plain list, never a 500.
func BuildTextQuery(raw string) string {
	return strings.Join(textTokens(raw), " AND ")
}

// BuildTitleQuery prepares the text filter for the ticket search box: the
// search matches ONLY the title (FTS5 column-filtered phrases) or an exact
// ticket ID/number (TKT-N). It returns the title-scoped MATCH expression
// ("" = no title filter) and the distinct positive ticket numbers found in
// raw. Description and comment bodies are never searchable (search-box
// scope: ID or title only).
func BuildTitleQuery(raw string) (string, []int64) {
	tokens := textTokens(raw)
	scoped := make([]string, 0, len(tokens))
	for _, token := range tokens {
		scoped = append(scoped, "title : "+token)
	}
	return strings.Join(scoped, " AND "), extractNumbers(raw)
}
