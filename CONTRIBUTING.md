# Contributing

tkt is a Go server with an embedded SQLite database, server-rendered templates, and a
Playwright suite for the browser. This guide covers local development, the test layers, and
the checks a change has to pass. The repository rules that go beyond this guide are in
[AGENTS.md](AGENTS.md), starting with its skill index.

## Before you start

Every change starts with a GitHub issue. The issue is the intent anchor for the work and
for the `Closes #` line in the pull request; there is no separate approval step. The full
rule and the label taxonomy are in
[issue-governance](.agents/skills/issue-governance/SKILL.md).

Work in a worktree so changes can run in parallel without sharing a database or a port.
From the existing checkout (the repository root):

```console
git fetch origin
git worktree add --track -b feat/123-short-slug ../tkt-worktrees/issue-123-short-slug origin/main
cd ../tkt-worktrees/issue-123-short-slug
mkdir -p data
```

Branch prefixes are `feat/`, `fix/`, `chore/`, `test/`, and `docs/`; the slug carries the
issue number. [worktree-isolation](.agents/skills/worktree-isolation/SKILL.md) explains the
per-worktree port and database rule.

## Local development

The Go version comes from `go.mod` (currently 1.26.10), so a toolchain that understands the
`go` directive is enough. Run the server from the checkout:

```console
TKT_LISTEN=127.0.0.1:8081 TKT_DB_PATH=data/tkt.db go run ./cmd/server
```

The store does not create the directory, so `data/` must exist. Open
`http://127.0.0.1:8081/setup` for the first account; `http://127.0.0.1:8081/healthz`
answers `ok` without signing in. The two environment variables and their defaults are in
[Deployment](docs/deployment.md#configuration).

The browser suite needs Node 24 (the CI version) and its own dependencies:

```console
cd e2e
npm ci
npm run install-browser
npm test
```

`npm test` starts an isolated server with a temporary database and a free loopback port, so
it does not touch `data/`. For manual exploration, `npm run server:start:seeded` starts one
with a seeded database and `npm run server:stop` stops it. [e2e/README.md](e2e/README.md)
describes the suite and its helpers.

## Test layers

The Go side is five layers, each with a different reality. Pick the lowest layer that can
prove the behavior; the conventions live in the tests themselves.

| Layer | Package | What is real |
| --- | --- | --- |
| Domain | `internal/domain` | Pure values and rules. No database, no HTTP. |
| Application | `internal/application` | Real services over in-memory port fakes. No SQLite. |
| SQLite adapter | `internal/adapters/sqlite` | The real driver, the embedded migrations, one isolated database per test. |
| HTTP | `internal/adapters/http` | A real file-backed database with every migration, real services, routes, and session middleware. |
| Process | `cmd/server` | The built binary run as a child process with its own database path. |

The browser layer is Playwright against Chromium. It owns structural baselines for the
canonical screens, selected critical journeys, and HTMX request evidence. It does not
replace domain or authorization tests. The boundary is stated in
[e2e/README.md](e2e/README.md); the Go conventions are in
[go-tests](.agents/skills/go-tests/SKILL.md).

## Checks

Run the focused package first, then the gate set. These are the local equivalents of the
workflows' commands:

```console
gofmt -l .
go mod tidy
git diff --exit-code -- go.mod go.sum
go vet ./...
go build ./...
go test ./... -race -count=1 -timeout 80m
```

- **Race is the expensive gate.** The workflow runs the full suite on `main` with an
  80-minute limit, which is the same run as the command above. Pull requests split it into
  shards and use `.github/scripts/race-fanout.sh` for the two slowest packages;
  `.github/workflows/quality.yml` is authoritative for the flags.
- **Coverage** is one global total:

  ```console
  go test ./... -covermode=atomic -coverprofile=coverage.out -count=1
  go tool cover -func=coverage.out
  ```

  The threshold is in `.github/workflows/coverage.yml`. A coverage run is not race
  evidence.
- **Static analysis** is `staticcheck ./...` and `govulncheck ./...`; the pinned versions
  and workflow are in `.github/workflows/security.yml`.
- **Golden files** are updated only after the handler and its assertions are right:
  `go test ./internal/adapters/http -run TestGolden -update`, then the same command without
  `-update`.
- **Frontend formatting** is `cd e2e && npm run format:check`. It covers the E2E sources and
  the static assets, not Markdown.
- **The container contract** is checked by `go test ./internal/container -count=1`. A change
  to the `Dockerfile`, `docker-compose.yml`, or the release workflow also needs a real
  build and a healthy container; see [container-governance](.agents/skills/container-governance/SKILL.md).

The workflows are the source of truth for every check:
`.github/workflows/quality.yml`, `.github/workflows/coverage.yml`,
`.github/workflows/security.yml`, `.github/workflows/e2e.yml`, and
`.github/workflows/container.yml`.

## Frontend, container, and documentation changes

- Templates, CSS, layout, responsive behavior, and accessibility follow
  [design-system](.agents/skills/design-system/SKILL.md). Its machine checks are part of the
  test layers above, and a visible change needs browser evidence.
- A browser-observable change follows [e2e-playwright](.agents/skills/e2e-playwright/SKILL.md):
  one canonical journey per behavior, updated rather than duplicated, with request evidence
  for HTMX.
- The image owns the runtime contract, and its consumers are checked by
  `internal/container`. [container-governance](.agents/skills/container-governance/SKILL.md)
  lists the fields and the commands that prove them.
- Documentation is Markdown under `docs/` plus this file. Keep one home per topic and link
  the others instead of repeating them. Check relative links when files move, and verify
  commands, flags, ports, and settings against the code and the container files before
  documenting them.

## Pull requests

A pull request links its issue with `Closes #<number>` and follows
[.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md). Labels are optional;
the issue carries the classification. The title becomes the squash-merge commit message, so
write it as a conventional commit with a scope, for example `docs(readme): add quick start`.

Every required check has to pass before merge. The definitions and the branch rules are in
[pr-governance](.agents/skills/pr-governance/SKILL.md) and `.github/workflows/`.

tkt is licensed under the Apache License 2.0; see [LICENSE](LICENSE).
