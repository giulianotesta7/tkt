package application

import (
	"context"
	"fmt"
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

// Search applies the actor's ticket access scope (ticket-access spec)
// BEFORE the filters: user → own tickets, agent → assigned, admin/root →
// full queue. An empty filter set returns all tickets within the actor's
// scope and never tickets outside it. The filters (AND), the D4-tokenized
// title/ID text filter, and pagination (page is 1-based; 0 behaves as 1)
// then compose on the scoped set.
func (s *SearchService) Search(ctx context.Context, actor domain.User, q TicketQuery, page int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	q, hasText := s.scoped(actor, q)
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

// Export sizes (issue #409). The export answers the SAME question the list
// does, for every match rather than one page, so it reads in large batches and
// hands each ticket to the caller as it arrives: the CSV is written while the
// read walks, and neither the service nor the handler ever holds the whole
// result set.
const (
	// ExportBatchSize is one read of the export walk.
	ExportBatchSize = 500
	// ExportMaxRows bounds one export. A desk with more matches than this has
	// a reporting problem a download cannot solve, and an unbounded walk would
	// let one request read the entire table.
	ExportMaxRows = 50000
)

// Export walks every ticket matching q WITHIN THE ACTOR'S SCOPE and calls visit
// once per ticket, in the query's order.
//
// It deliberately goes through the same scoped() preparation Search and Count
// use, so an export can never return a row its own list would hide: a requester
// exports their own active work, an agent what their scope allows, staff the
// whole queue. visit returning an error stops the walk and is returned as is.
func (s *SearchService) Export(ctx context.Context, actor domain.User, q TicketQuery, visit func(domain.Ticket) error) error {
	scoped, hasText := s.scoped(actor, q)
	for offset := 0; offset < ExportMaxRows; offset += ExportBatchSize {
		limit := ExportBatchSize
		if remaining := ExportMaxRows - offset; remaining < limit {
			limit = remaining
		}
		var batch []domain.Ticket
		var err error
		if hasText {
			batch, err = s.search.Search(ctx, scoped, Page{Offset: offset, Limit: limit})
		} else {
			batch, err = s.tickets.List(ctx, scoped, Page{Offset: offset, Limit: limit})
		}
		if err != nil {
			return err
		}
		for i := range batch {
			if err := visit(batch[i]); err != nil {
				return err
			}
		}
		if len(batch) < limit {
			return nil
		}
	}
	return nil
}

// scoped applies the actor's scope, the requester's fixed active-work
// restriction and the title/ID tokenization: the preparation Search and Count
// share, so a tab count can never disagree with the list it opens.
func (s *SearchService) scoped(actor domain.User, q TicketQuery) (TicketQuery, bool) {
	q = readQuery(actor, q)
	if actor.Role == domain.RoleUser {
		// A requester reads their own work only, and the SERVER chooses which
		// part of it: ignore every caller-supplied state so a crafted query
		// string can neither reveal history outside the folded section nor
		// choose a section. The chosen section is translated here, so no store
		// adapter has to know about presentation sections (issue #411).
		q.State = nil
		q.States, q.OnHold, q.NotOnHold = requesterSectionScope(q.Section, q.OnHold)
		q.Section = TicketSectionAll
	}
	q.Text, q.Numbers = BuildTitleQuery(q.Text)
	return q, q.Text != "" || len(q.Numbers) > 0
}

// requesterSectionScope translates one requester presentation section into the
// store predicates that define it (issue #411, S3/S4). An unknown or absent
// section keeps the pre-#411 behavior — active work only, with the caller's
// own on-hold filter (the CSV export's view=on_hold) — so a caller that never
// names a section can never widen a requester's read. Every arm returns a
// fresh slice, so no caller can mutate another query's state set.
func requesterSectionScope(section TicketSection, callerOnHold bool) (states []domain.State, onHold, notOnHold bool) {
	switch section {
	case TicketSectionRequesterWaiting:
		return []domain.State{domain.StateNew, domain.StateInProgress}, true, false
	case TicketSectionRequesterClosed:
		return []domain.State{domain.StateResolved, domain.StateClosed}, false, false
	case TicketSectionRequesterOpen:
		return []domain.State{domain.StateNew, domain.StateInProgress}, false, true
	default:
		return []domain.State{domain.StateNew, domain.StateInProgress}, callerOnHold, false
	}
}

// Count answers how many tickets Search would list for the same actor and
// query (issue #382, the queue tab counts), with exactly Search's scope.
func (s *SearchService) Count(ctx context.Context, actor domain.User, q TicketQuery) (int, error) {
	q, hasText := s.scoped(actor, q)
	if hasText {
		return s.search.SearchCount(ctx, q)
	}
	return s.tickets.Count(ctx, q)
}

// AgentQueueContext batches the agent queue row context for one page of
// tickets (issue #122) through the OPTIONAL AgentQueueContextStore capability,
// discovered by type assertion on the ticket store port: existing stores and
// fakes stay untouched, while a store without the capability is a wiring error
// that fails with a clear message instead of fabricated empty rows.
func (s *SearchService) AgentQueueContext(ctx context.Context, tickets []domain.Ticket) (map[int64]AgentTicketRowContext, error) {
	if len(tickets) == 0 {
		return map[int64]AgentTicketRowContext{}, nil
	}
	store, ok := s.tickets.(AgentQueueContextStore)
	if !ok {
		return nil, fmt.Errorf("application: agent queue context requires a ticket store implementing AgentQueueContextStore")
	}
	ids := make([]int64, 0, len(tickets))
	for _, t := range tickets {
		ids = append(ids, t.ID)
	}
	return store.AgentQueueContext(ctx, ids)
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
