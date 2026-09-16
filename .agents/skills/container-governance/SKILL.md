---
name: container-governance
description: "Trigger: Dockerfile, docker-compose, container image, healthcheck, data volume, or image publishing work. Govern the container contract as a deliberate pair and prove alignment with the machine check in internal/container."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Activate when the work touches:

- the `Dockerfile` or the container image it builds (base images, build flags, labels, publishing arguments);
- `docker-compose.yml` or the way the image is run;
- `.dockerignore` and what enters the build context;
- `.github/workflows/container.yml` and the CI smoke test;
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
4. **One contract change updates every consumer in the same change.** The Dockerfile, compose, the CI workflow and the alignment table below move together, or the change is not done.
5. **The machine check in `internal/container` is what proves alignment, and CI runs it.** Point at the check instead of restating its rules; if a new value needs guarding, extend the check rather than writing prose about it.

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
| Image name | `go.mod` module path, published to GHCR | `docker-compose.yml` (image reference) |

## Decision Gates

| Condition | Action |
| --- | --- |
| A value is declared on both sides of the contract | Remove the duplicate, or extend the machine check to cover it and let the test decide |
| The data path or the port moves | Update the Dockerfile, the compose volume, the CI workflow and the prepared directory in the same change |
| The check cannot parse a file | Fail loudly. Never add a skip rule without a stated reason and a stated blind spot |
| The publishing path changes | Update the compose image reference and re-derive the GHCR expectation from the go.mod module path |
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
- `../issue-governance/SKILL.md` — the issue-first gate every change passes through
- `../go-tests/SKILL.md` — the Go test layers the machine check lives in
