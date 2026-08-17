# Delta for Ticket Search

## MODIFIED Requirements

### Requirement: Search by ID or Title

The system MUST expose the existing `GET /tickets?q=` ID/title search beside “New ticket” for every role. Search MUST remain scoped by actor access and MUST NOT match descriptions or comments.
(Previously: search was defined through the canonical filter bar without requiring an all-role compact entry point.)

#### Scenario: Every role searches
- GIVEN an authenticated actor of any role
- WHEN the ticket list renders
- THEN an ID/title search control appears beside “New ticket” and submits through `/tickets?q`

#### Scenario: Search remains scoped
- GIVEN a user-owned ticket and another user’s matching ticket
- WHEN a `user` searches the matching term
- THEN only their own matching ticket is returned

## MODIFIED Requirements

### Requirement: Canonical Filter Surface

Staff roles MUST retain the full filter bar for text, state, priority, category, and assigned user. The `user` role MUST NOT receive that filter bar; its compact search MUST remain limited to its own-ticket scope. No role MAY render duplicate text-search controls.
(Previously: one filter bar was canonical for every role.)

#### Scenario: Staff retain filters
- GIVEN an `agent`, `admin`, or `root`
- WHEN the ticket list renders
- THEN the full filter bar remains available

#### Scenario: User loses filters
- GIVEN a `user` actor
- WHEN the ticket list renders and searches
- THEN the filter bar is absent and results remain own-ticket scoped
