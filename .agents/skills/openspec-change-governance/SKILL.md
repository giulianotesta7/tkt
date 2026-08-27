---
name: openspec-change-governance
description: "Trigger: adding or modifying observable behavior, domain rules, authorization, endpoints, forms, flows, persistence, or contracts; or any OpenSpec change proposal/delta/archive. Enforce spec–implementation–test alignment and validate with the official OpenSpec CLI."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Run when a TKT change:
- adds or modifies observable behavior;
- alters domain rules, authorization, or workflow states;
- modifies endpoints, forms, flows, persistence, or contracts;
- implements a proposal or delta for an OpenSpec change;
- prepares close or archive of an OpenSpec change.

Do NOT run for:
- refactors with no observable behavior change;
- internal test optimizations;
- CI-only changes;
- formatting;
- documentation with no contractual impact.

## Hard Rules

- Before each work unit, read the relevant spec, proposal, design, and tasks from `openspec/`. Verify the code is scoped to the active change and does not contradict its spec.
- If implementation, tests, and specs contradict each other, BLOCK the work unit. Do not proceed until alignment is restored.
- Before closing any work unit that touches observable behavior, run `openspec validate --all --strict --no-interactive` and confirm it passes. Also run `openspec validate --archived --no-interactive`.
- Canonical specs under `openspec/specs/` MUST only be modified by the `sdd-archive` phase while synchronizing an approved delta. All other phases MUST describe behavioral changes under `openspec/changes/`.
- Gentle AI's `sdd-spec`, `sdd-apply`, `sdd-verify`, and `sdd-archive` skills own their respective phases. This skill does not replace or redefine them.

## Decision Gates

| Condition | Result |
| --- | --- |
| Change has no observable behavior impact | SKIP with reason |
| No relevant spec or change found | SKIP — create one if scope warrants it |
| Specs and implementation contradict each other | BLOCKED — align before proceeding |
| `openspec validate --all --strict --no-interactive` fails | BLOCKED with validation errors |
| `openspec validate --archived --no-interactive` fails | BLOCKED with validation errors |

## Execution Steps

1. **Identify affected specs**: scan `openspec/specs/` and `openspec/changes/*/specs/` for specs whose domain matches the change. List each matched spec with its path and scope.
2. **Assess delta needs**: determine whether the change warrants a new spec delta, an update to an existing change, or an archive. If no change exists, evaluate whether the scope justifies creating one.
3. **Inspect alignment**: for each affected spec, verify that the implementation satisfies every Requirement and Scenario. Check that tests cover the scenarios. If code or tests contradict the spec, flag the inconsistency.
4. **Validate**: run `openspec validate --all --strict --no-interactive` and `openspec validate --archived --no-interactive` against the final state. Report results verbatim.
5. **Close or archive**: on passing validation, proceed with commit/PR/archive. On failure, surface the errors and block.

## Output Contract

Return:
- `applicable`: yes / no / skipped — and the reason;
- `specs_inspected`: list of paths and the change they belong to;
- `files_updated`: list of files changed (or reason they were not);
- `validation_result`: output of the two OpenSpec validation commands;
- `inconsistencies`: any contradictions found between specs, implementation, and tests;
- `blocked`: true / false — and the reason if blocked.

## References

- `../../../openspec/config.yaml` — project's OpenSpec configuration and rules.
- `../../../openspec/specs/` — canonical specifications.
- `../../../openspec/changes/` — active change deltas.
- `../../../.github/workflows/openspec.yml` — CI validation workflow.