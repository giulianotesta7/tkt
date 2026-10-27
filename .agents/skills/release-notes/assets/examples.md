# Release-note examples

These changes and versions are hypothetical, not implemented features or a roadmap.
Use source evidence, never these bullets, when drafting a real release.
Title lines are release metadata, not headings to repeat in the body.
The examples use readable main-branch documentation links; pin real release links to verified source refs and replace the illustrative comparison range with a real link.

## Fixes-only release

Title: `v0.1.1`

Body:

```markdown
## Fixes

- Preserve queue filters when returning from a ticket.
- Handle attachment filenames containing accented characters correctly.
- Refresh the ticket's SLA indicator immediately after resuming work.

**Full changelog:** `v0.1.0...v0.1.1`

[Documentation](https://github.com/giulianotesta7/tkt#documentation) | [Español](https://github.com/giulianotesta7/tkt/blob/main/README.es.md)
```

## Features and fixes

Title: `v0.2.0`

Body:

```markdown
## Features

- **Saved queue views:** save named filter combinations to quickly return to frequently used queues.
- **Bulk assignment:** select multiple tickets and assign them to a desk in one action.

## Fixes

- Preserve queue filters when returning from a ticket.
- Handle attachment filenames containing accented characters correctly.

**Full changelog:** `v0.1.0...v0.2.0`

[Documentation](https://github.com/giulianotesta7/tkt#documentation) | [Español](https://github.com/giulianotesta7/tkt/blob/main/README.es.md)
```

For a first release, introduce verified product capabilities and include Getting started.
Do not invent a previous release or a comparison range.
For later releases, omit Getting started unless installation instructions change.
Put required upgrade actions in Upgrade notes before Features/Fixes.
Do not add Highlights merely to repeat Features, or an empty category.
