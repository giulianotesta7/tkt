# Proposal: Guard unsaved admin drawers

## Intent
Prevent administrators from losing pending values while closing User, Department, Desk, or Category drawers.

## Scope
- Extend the existing Users confirmation pattern to Department and Desk create and edit drawers, while retaining Category coverage.
- Track only fields that each drawer can submit: Category name, description, department, and desk; Department name and description; Desk name, description, and department; User create name, email, and password; User edit name, email, role, and active state.
- Keep a drawer dirty after a server-rendered validation error. A successful save closes the drawer without a confirmation.
- Preserve pending Desk name, description, and department values across successful member add or remove drawer replacements for that same Desk only.
- Keep direct drawer URLs, HTMX swaps, native form fallback, focus restoration, and the existing Users dialog wording and destructive styling.
- In the Desk edit drawer only, keep the profile actions with the profile form and separate Members with whitespace and its heading. Member controls state that changes save immediately.

## Supersession
This change supersedes the active `category-unsaved-guard` delta's category-only restriction. It does not change canonical specs or archive that earlier delta.

## Non-goals
- Browser unload, reload, or tab-close protection.
- Workflow autosave protection.
- Membership rollback when a user discards Desk form edits.
- Domain, authorization, persistence, endpoint, or shared frontend abstraction changes.
- Changes to User, Department, or Category drawer layout, typography, palette, buttons, or form behavior.

## Rollback
Remove the client-side guards and dialog markup from the drawer assets. Existing server routes, server validation, membership mutations, and native form submissions remain available.
