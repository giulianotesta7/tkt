# Delta for Ticket Search

## MODIFIED Requirements

### Requirement: Composable Filters

The system MUST filter tickets by state, priority, category, and assigned user, and MUST compose all active filters with AND semantics. The actor's ticket access scope (see Ticket Access and Assignment) MUST be applied before any filter: `user` searches own tickets, `agent` assigned tickets, `admin`/`root` the full queue. An empty filter set MUST return all tickets within the actor's scope and never tickets outside it. (Previously: an empty filter set returned all tickets with no access-scope restriction.)

#### Scenario: Filter composition

- GIVEN tickets across states, priorities, categories, and users, within an `admin`'s scope
- WHEN the admin filters by state `resolved`, priority `high`, category "Bugs", and a specific user
- THEN only tickets matching all four conditions are returned

#### Scenario: Empty filters respect actor scope

- GIVEN tickets created by actors A and B
- WHEN A, a `user`-role actor, lists tickets with no filters
- THEN only tickets created by A are returned
- AND no tickets created by B appear

#### Scenario: Agent search is scoped to assignment

- GIVEN tickets assigned to agents X and Y
- WHEN agent X searches by a title term matching one of Y's tickets
- THEN Y's ticket is not returned
- AND only X's assigned tickets can match