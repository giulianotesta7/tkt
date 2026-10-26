# Integrated design patterns

Use these sources to extend an existing pattern, not to force every screen into the
same composition. Preserve each screen's domain and interaction contract.

## Page hierarchy and density

- Start from a clear title and contextual actions, then controls and content.
  Notifications uses the command header followed by one list surface:
  `web/templates/pages/notifications.html`.
- For administrative lists, keep heading, tabs, search, and table in one readable
  flow rather than adding a permanent explanatory sidebar. Users is the example:
  `web/templates/partials/users_screen.html`.
- Group preferences by purpose with actions local to their form; do not infer a
  global save action from visual similarity:
  `web/templates/pages/preferences.html`.
- Separate compact summary metrics from detailed analysis. Reuse the existing KPI,
  panel, filter, and chart/table structures rather than inventing another card family:
  `web/templates/partials/ticket_metrics.html` and
  `web/templates/static/ticket_metrics.css`.

## Focused editing

- Users editing progresses from identity to role to account lifecycle, with save/cancel
  actions at the drawer foot. Preserve dialog labeling, errors, dirty confirmation,
  focus handling, and narrow-screen reachability:
  `web/templates/partials/user_drawer.html` and `web/templates/static/users.js`.
- Drawer geometry is scoped in `web/templates/static/users.css`. Shared
  users-prefixed classes also serve `web/templates/partials/category_drawer.html`;
  keep Users-only overrides under its host rather than altering every drawer.
- The workflow builder places version facts and clone controls above a step rail and
  selected-step editor. Preserve the separate clone form, dirty baseline, publication
  controls, and non-drag alternatives; do not turn this specialized editor into a
  mandatory two-pane form pattern:
  `web/templates/partials/workflow_builder.html`.

## Deliberate form interactions

- Reuse the ticket-property picker in `web/templates/partials/listbox.html`.
  Options are submit buttons; opening, navigating, or searching is not a save.
  Preserve the no-script option path. This does not replace ordinary form selects.
- Reuse written SLA duration fields and their per-field reading/error feedback in
  category and Settings contexts:
  `web/templates/partials/sla_duration_grid.html` and
  `web/templates/static/sla_duration.js`. Do not replace them with separate
  hour/minute/second inputs or reduce their existing precision.
- Keep labels and useful validation or consequence guidance; remove obvious repetition,
  not information necessary to operate the form.

## Responsive and accessible proof

- Check content and footer reachability in narrow drawers, and clipping/overflow in
  lists and metric panels. Keep deliberate scrolling at the correct container;
  hiding overflow is not a layout fix.
- Preserve labels, focus visibility, keyboard operation, status/error announcements,
  and existing non-JavaScript routes when rearranging markup.
- Reuse the measured layout gates in `e2e/tests/helpers/layout.ts`; inspect the
  component's own scroll/client dimensions and computed styles in both themes.
