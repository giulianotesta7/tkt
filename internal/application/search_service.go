package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// PageSize is the fixed pagination window (D2): 10 per page, no env knob.
const PageSize = 10

// SearchService implements list and search use cases (ticket-search spec):
// composable AND filters, FTS text search with D4 tokenization, stable
// pagination, and summary chips reflecting the filtered result set.
type SearchService struct {
	tickets TicketStore
	search  SearchStore
}

// NewSearchService wires list/search against the given ports.
func NewSearchService(tickets TicketStore, search SearchStore) *SearchService {
	return &SearchService{tickets: tickets, search: search}
}

// SearchResult is the list-view payload: the page's tickets, the total
// number of matches, the requested page, and the chips counts.
type SearchResult struct {
	Tickets    []domain.Ticket
	Total      int
	Page       int
	ByState    map[domain.State]int
	ByPriority map[domain.Priority]int
}

// Search applies the filters (AND), the D4-tokenized text filter, and
// pagination (page is 1-based; 0 behaves as 1). Chips always reflect the
// filtered result set, text included.
func (s *SearchService) Search(ctx context.Context, q TicketQuery, page int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	q.Text = BuildTextQuery(q.Text)
	p := Page{Offset: (page - 1) * PageSize, Limit: PageSize}

	result := &SearchResult{Page: page}
	var err error
	if q.Text == "" {
		result.Tickets, err = s.tickets.List(ctx, q, p)
	} else {
		result.Tickets, err = s.search.Search(ctx, q, p)
	}
	if err != nil {
		return nil, err
	}
	if q.Text == "" {
		result.Total, err = s.tickets.Count(ctx, q)
	} else {
		result.Total, err = s.search.SearchCount(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	result.ByState, err = s.tickets.CountsByState(ctx, q)
	if err != nil {
		return nil, err
	}
	result.ByPriority, err = s.tickets.CountsByPriority(ctx, q)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// BuildTextQuery tokenizes raw user input for FTS5 (D4): each whitespace
// token is double-quoted with embedded quotes escaped, then joined with AND.
// Quotes-only tokens carry no content and are dropped; when nothing remains
// the query is empty, which means NO text filter — invalid input degrades to
// a plain list, never a 500.
func BuildTextQuery(raw string) string {
	tokens := strings.Fields(raw)
	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		escaped := strings.ReplaceAll(token, `"`, `""`)
		if strings.Trim(escaped, `"`) == "" {
			continue
		}
		quoted = append(quoted, `"`+escaped+`"`)
	}
	return strings.Join(quoted, " AND ")
}
