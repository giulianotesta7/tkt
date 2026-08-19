# Delta for Role-Specific Views

## MODIFIED Requirements

### Requirement: Capability-Gated Navigation

The shell MUST show Desks, not Groups, to authorized `admin` and `root` actors, with an accessible desk/table SVG icon. Users MUST see no desk link. Ticket-list controls MUST follow role-specific search/filter rules while server authorization remains unchanged.
(Previously: authorized navigation used Groups terminology and a letter icon.)

#### Scenario: Desk navigation
- GIVEN an admin or root
- WHEN the shell renders
- THEN a `/desks` link labeled “Desks” has an accessible SVG icon

#### Scenario: User has compact ticket search
- GIVEN a user-role actor
- WHEN the ticket list renders
- THEN ID/title search is present and the full filter bar is absent
