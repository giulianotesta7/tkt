---
name: worktree-isolation
description: "Trigger: starting any change in the tkt repository. Create a new git worktree under ../tkt-worktrees instead of working in the main checkout, so several changes can run in parallel."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "2.0"
---

## Activation Contract

Run before the first write of any change — feature, bug, chore, docs, or skill edit. Not needed for read-only work: reading, searching, inspecting history, running tests, or building all leave the main checkout usable.

## Hard Rules

- One change, one worktree, one branch. The main checkout is for reading, syncing, and merging.
- The worktree goes in the sibling directory next to the repository root, never inside it: `../tkt-worktrees/<issue-slug>`.
- Directory issue-<n>-<slug>, branch <type>/<n>-<slug>, based on an updated default branch.
- The governing issue comes first: `../issue-governance/SKILL.md` states the requirement, and `../pr-governance/SKILL.md` holds the branch prefixes.
- One worktree, one port. Two default servers both want the port `:8080`.

## Execution Steps

1. Create the worktree from the updated base. `--track` sets the upstream, so a later push needs no `-u`:

   ```
   git fetch origin
   git worktree add --track -b <type>/<n>-<slug> ../tkt-worktrees/issue-<n>-<slug> origin/main
   cd ../tkt-worktrees/issue-<n>-<slug>
   ```

2. Create the database directory. The store does not create it, and the server fails with `unable to open database file (14)` without it:

   ```
   mkdir -p data
   ```

3. Run with this worktree's own port and database, so two worktrees never fight over `:8080` or share one database file:

   ```
   TKT_LISTEN=127.0.0.1:8081 TKT_DB_PATH=data/tkt.db go run ./cmd/server
   ```

   Playwright needs none of that: it picks a free port and a temporary database on its own.

## Output Contract

Report the worktree path, its branch, the base commit, and the TKT_LISTEN and TKT_DB_PATH pair in use.

## References

- `../../../AGENTS.md` — skill registration.
- `../issue-governance/SKILL.md` — the issue requirement and label taxonomy.
- `../pr-governance/SKILL.md` — branch prefixes.
- `cmd/server/main.go` — the TKT_LISTEN and TKT_DB_PATH defaults.
- `internal/adapters/sqlite/sqlite.go` — the store Open that leaves the database directory to the operator.
