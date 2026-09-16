---
name: container-governance
description: "Trigger: Dockerfile, docker-compose, container image, healthcheck, data volume, or image publishing work. Govern the container contract as a deliberate set and prove alignment with the machine check in internal/container."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.1"
---

## Activation Contract

Activate when the work touches:

- the `Dockerfile` or the container image it builds (base images, build flags, labels, publishing arguments);
- `docker-compose.yml` or the way the image is run;
- `.dockerignore` and what enters the build context;
- `.github/workflows/container.yml` and the CI smoke test;
- `.github/workflows/release-container.yml` and the publish on a `vX.Y.Z` tag push;
- the image contract itself: environment defaults, listen port, data path, run-as user, healthcheck;
- image publishing: tags, the registry path, or the OCI labels.

Do NOT activate for:

- Go application behavior with no container impact (that is `go-tests`);
- browser journeys or visual baselines (that is `e2e-playwright`);
- Engram artifact bookkeeping on its own (that is `engram-governance`).

## Hard Rules

1. **The image owns the runtime contract.** The `Dockerfile` declares the environment defaults, the exposed port, the run-as user, the stop signal, the entrypoint binary and the healthcheck, all in JSON exec form so the check can read them. Consumers inherit them; they do not restate them.
2. **Compose must not become a second source of truth.** `docker-compose.yml` holds what compose owns: the project name, the service, the image reference, the build target, the port mapping, the named volume and the restart policy. Restating an image-owned value is tolerated only when it is identical: the machine check reports an identical environment duplicate and fails a contradiction. Compose service keys are allowlisted by name in the check, so a key that could silently bypass the image contract fails until someone allows it deliberately with a reason. The compose file must also declare `build` with context "." — the repository's compose must be able to build the image it names, so an image-only compose file cannot pass the check; a pull-only compose file would need its own consumer entry in the check, not a weakened rule.
3. **The prepared data directory is non-negotiable.** The build stage creates the data directory and chowns it to UID 65532, and the runtime stage copies it with an explicit chown, because a fresh named volume inherits that ownership on first mount. Removing it is the restart loop native review already caught once.
4. **One contract change updates every consumer in the same change.** The Dockerfile, compose, the CI workflows and the alignment table below move together, or the change is not done.
5. **The machine check in `internal/container` is what proves alignment, and CI runs it.** Point at the check instead of restating its rules; if a new value needs guarding, extend the check rather than writing prose about it.

### Release contract

The image is published to GHCR only when the maintainer pushes a `vX.Y.Z` tag (issue #208); there is no rolling publish from `main`. The workflow's tag filter (`v*.*.*`) is deliberately broad: GitHub filter patterns are glob-shaped rather than regular expressions, so the tag grammar cannot be expressed in a filter — the no-leading-zeros rule needs alternation, which glob has none of — and a filter that matches nothing fails silently. The workflow itself is the single authority on the shape: it validates the tag and fails loudly on a malformed one. A tag without the leading `v` does not trigger the workflow at all — the repository's tag convention is `vX.Y.Z`.

The release workflow `.github/workflows/release-container.yml` builds once and publishes three tags — the full version, the minor (`vX.Y`), and `latest` — and needs `packages: write` for `GITHUB_TOKEN` to push to GHCR. A failing smoke test publishes nothing: the image is built and smoke-tested in the same run before any login or push, and release runs are serialized with `cancel-in-progress: false` because a release must never be cancelled mid-push. The release workflow is a checked consumer of the contract, but the machine check in `internal/container` proves exactly three things about it: the image repository is derived from the repository path and lowercased, the `VERSION` and `REVISION` build args are passed, and the smoke test's env values, volume target, port and healthcheck invocation agree with the image. The publish order, the push target, the trigger, the permissions and the third-party actions are stated blind spots of the check, recorded here as decisions — do not assume the check guards them. The list is deliberately the notable ones, not an exhaustive audit: the smoke test's own shell guards (the health-endpoint wait, the UID assertion, the database-ownership assertion), the manifest verification, the cleanup trap and the build-arg values are also unguarded by the check.

### Alignment table

| Field | Owner | Consumers |
| --- | --- | --- |
| Environment defaults (TKT_DB_PATH, TKT_LISTEN) | `Dockerfile` | `docker-compose.yml` (inherits), `.github/workflows/container.yml` (smoke test) |
| Listen port | `Dockerfile` (EXPOSE and TKT_LISTEN) | `docker-compose.yml` (container side of the mapping), `.github/workflows/container.yml` |
| Data path | `Dockerfile` (prepared directory and TKT_DB_PATH) | `docker-compose.yml` (volume target), `.github/workflows/container.yml` (volume target) |
| Run-as user | `Dockerfile` (USER, chown) | `docker-compose.yml` (must not override) |
| Stop signal | `Dockerfile` (STOPSIGNAL) | `docker-compose.yml` (where the key is rejected rather than overridden) |
| Healthcheck | `Dockerfile` (HEALTHCHECK) | `docker-compose.yml` (inherits; only an identical restatement passes), `.github/workflows/container.yml` (binary check) |
| Entrypoint binary | `Dockerfile` (ENTRYPOINT) | `docker-compose.yml` (command and entrypoint rejected) |
| Image name | `go.mod` module path, published to GHCR | `docker-compose.yml` (image reference), `.github/workflows/release-container.yml` (derived, lowercased image reference) |

## Decision Gates

| Condition | Action |
| --- | --- |
| A value is declared on both sides of the contract | Remove the duplicate, or extend the machine check to cover it and let the test decide |
| The data path or the port moves | Update the Dockerfile, the compose volume, the CI workflow and the prepared directory in the same change |
| The check cannot parse a file | Fail loudly. Never add a skip rule without a stated reason and a stated blind spot |
| The publishing path changes | Update the compose image reference and the release workflow, and re-derive the GHCR expectation from the go.mod module path |
| A run seems to need an env or user override in compose | Stop: an override that differs from the image fails the check on purpose. Decide which side owns the value first |
| Base image tags or digests are proposed | The builder pins the exact Go patch tag and the runtime base uses the distroless nonroot tag; no digest is pinned, because nothing automates it |

## Execution Steps

1. Change both sides of the contract in the same edit: the Dockerfile and every consumer the alignment table names.
2. Run the machine check: `go test ./internal/container -count=1`.
3. Run the closing gate: `go test ./... -race -count=1`.
4. Prove the change runs: `docker build`, then start a container against a throwaway named volume and wait for healthy before calling container work done.
5. Report the exact commands and their results, including the build and smoke test.

## Output Contract

Report: the files changed on each side of the contract, the focused and full test commands with results, the build and smoke test commands with results, any invariant the check does not yet cover, and anything skipped or blocked. State plainly when a gate was not run.

## References

- `../../../AGENTS.md` — the skill index this file is registered in
- `../../../internal/container/container_test.go` — the machine check and its stated blind spots
- `../../../Dockerfile` — the image contract
- `../../../docker-compose.yml` — the compose contract
- `../../../.github/workflows/container.yml` — the CI smoke test
- `../../../.github/workflows/release-container.yml` — the release publish workflow
- `../issue-governance/SKILL.md` — the issue-first gate every change passes through
- `../go-tests/SKILL.md` — the Go test layers the machine check lives in
