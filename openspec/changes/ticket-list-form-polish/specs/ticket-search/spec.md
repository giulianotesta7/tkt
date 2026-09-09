# Ticket Search Specification Delta

## ADDED Requirements

### Requirement: Unified ticket query toolbar

The ticket list MUST render one toolbar containing the ID/title search and every filter visible to the actor. A staff actor MUST submit its text, state, priority, category, and assignee controls in one request through `GET /tickets`. A `user` actor MUST receive only the scoped ID/title search. Applying a query, pressing Enter in the search input, clearing the toolbar, and following pagination MUST preserve the existing actor scope and query semantics. Apply and Clear MUST use page 1.

#### Scenario: Staff applies a combined query

- GIVEN an authorized staff actor viewing the ticket list
- WHEN the actor submits text, state, priority, category, and assignee controls
- THEN the request contains every visible control and page 1
- AND the results retain the existing AND semantics and actor scope

#### Scenario: Query persists through page navigation

- GIVEN a filtered result with multiple pages
- WHEN the actor follows Next or Prev
- THEN the URL contains the active query parameters and the requested page
- AND the toolbar controls render those active values

### Requirement: Ticket list result presentation

The ticket list MUST show the correctly pluralized actor-scoped result count beneath the Tickets heading. It MUST show `No tickets yet` for an empty unfiltered scoped queue and `No tickets match your filters` for an empty active query. Pagination MUST be absent when the result has at most one page. The table and toolbar MUST remain usable without document horizontal overflow at 390px, while retaining ticket ID, title, state, priority, date, and available actions.

#### Scenario: Empty scoped queue

- GIVEN an actor with no visible tickets and no active query
- WHEN the ticket list renders
- THEN the heading shows `0 tickets`
- AND the list says `No tickets yet`
- AND no pagination markup renders

#### Scenario: Filtered empty result

- GIVEN an actor with an active query that matches no visible tickets
- WHEN the ticket list renders
- THEN the list says `No tickets match your filters`
- AND the Clear control remains available
