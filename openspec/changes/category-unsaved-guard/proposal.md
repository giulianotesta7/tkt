# Proposal: Guard unsaved Category drawer changes

## Intent
Prevent an administrator from accidentally discarding changes in Category create and edit drawers.

## Scope
- Warn only when a Category drawer has unsaved changes and the user closes it, cancels it, clicks its backdrop, presses Escape, or uses browser Back.
- Compare Category name, description, Department, and Desk with the initial filtered form values. Reverted values are clean.
- Match the Users unsaved-change dialog wording and destructive visual treatment while retaining Category-only behavior attributes. A successful Category save closes without a warning.
- Keep server-side validation, HTMX error rendering, direct drawer routes, and native form fallback unchanged.

## Non-goals
- Guards for Department or Desk drawers, beforeunload protection, count or status-help changes, validation-marker changes, and catalog layout changes.

## Rollback
Remove the Category-only client guard and dialog markup. The existing server routes and native forms remain usable without it.
