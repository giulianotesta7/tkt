# Delta for Category Management

## ADDED Requirements

### Requirement: Workflow-Based Category Availability

The system MUST make a category available for new tickets only while it has a published workflow version. Ticket-creation category choices MUST exclude categories without a published version for every role. The managed category list MUST show `Published vN` when the current draft matches the published definition and `Draft` when an editable draft differs from the active published definition. A category with no workflow definition MUST have no workflow badge and MUST remain unavailable for new tickets.

#### Scenario: Unconfigured category is unavailable

- GIVEN a category with no workflow definition or published version
- WHEN any authenticated actor opens the new-ticket form
- THEN that category is absent from the category choices

#### Scenario: Published category is available

- GIVEN a category with a current published workflow version
- WHEN any authenticated actor opens the new-ticket form
- THEN that category is available as a category choice

#### Scenario: Draft edits retain published availability

- GIVEN a category with published version 2 and a draft that differs from version 2
- WHEN an `admin` or `root` views the managed category list
- THEN the category is marked `Draft`
- AND it remains available for new tickets through published version 2

#### Scenario: Category with no workflow has no badge

- GIVEN an existing category for which workflow configuration has never begun
- WHEN an `admin` or `root` views the managed category list
- THEN the category has no workflow badge
- AND it remains unavailable for new tickets
