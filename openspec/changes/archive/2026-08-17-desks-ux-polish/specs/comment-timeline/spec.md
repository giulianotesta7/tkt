# Delta for Comment Timeline

## MODIFIED Requirements

### Requirement: Newest-First Timeline

The timeline MUST preserve newest-first ordering and server-side disclosure. Internal comments MUST use a distinct visual treatment from public comments, without exposing internal content to users.
(Previously: comments and audit events had distinct treatment, but internal-comment styling was unspecified.)

#### Scenario: Internal comment styling
- GIVEN a staff-visible timeline with public and internal comments
- WHEN it renders
- THEN internal and public comments have distinguishable backgrounds
- AND ordering remains newest first
