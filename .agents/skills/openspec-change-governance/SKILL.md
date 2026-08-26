---
name: openspec-change-governance
description: "Trigger: adding or modifying observable behavior, domain rules, authorization, endpoints, forms, flows, persistence, contracts, or any OpenSpec change proposal/delta/archive. Enforce spec-implementation-test alignment and validate with openspec validate --all --strict."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Run when a change:
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

- Load the project's relevant spec, proposal, design, and tasks from `openspec/` before each work unit. Verify that the code you are about to write or review is scoped to the active change and does not contradict its spec.
- Express every scenario in Given/When/Then form.
- After any change that modifies a spec'd contract, update or sync the affected spec, proposal, or delta before closing the work unit.
- Before accepting a completion or archive, run `openspec validate --all --strict` and confirm it passes without errors.
- Never mutate canonical specs (`openspec/specs/`) unless you find a genuine inconsistency directly related to the active PR, and in that case stop and report before expanding scope.

## Decision Gates

| Condition | Result |
| --- | --- |
| Change has no observable behavior impact | SKIP with reason |
| No relevant spec or change found | SKIP — create one if scope warrants it |
| Specs are out of date with implementation | BLOCKED — sync specs before proceeding |
| `openspec validate --all --strict` fails | BLOCKED with validation errors |
| Implementation contradicts spec scenarios | BLOCKED — align one or the other |
| New behavior needs new spec | BLOCKED — create proposal or spec first |

## Execution Steps

1. **Identify affected specs**: scan `openspec/specs/` and `openspec/changes/*/specs/` for specs whose domain matches the change. List each matched spec with its path and scope.
2. **Assess delta needs**: determine whether the change warrants a new spec delta, an update to an existing change, or an archive. If no change exists, evaluate whether the scope justifies creating one.
3. **Inspect alignment**: for each affected spec, verify that the implementation satisfies every Requirement and Scenario. Check that tests cover the scenarios. If code or tests contradict the spec, flag the inconsistency.
4. **Sync or warn**: if the change is within the active OpenSpec change's scope and the spec already captures the intent, mark it as aligned. If the spec is missing new requirements, block the work unit and propose the update.
5. **Validate**: run `openspec validate --all --strict` against the final state. Report the result verbatim.
6. **Close or archive**: on a passing validate, proceed with commit/PR/archive. On failure, surface the errors and block.

## Output Contract

Return:
- `applicable`: yes / no / skipped — and the reason;
- `specs_inspected`: list of paths and the change they belong to;
- `files_updated`: list of files changed (or reason they were not);
- `validation_result`: output of `openspec validate --all --strict`;
- `inconsistencies`: any contradictions found between specs, implementation, and tests;
- `blocked`: true / false — and the reason if blocked.

## References

- `../../../openspec/config.yaml` — project's OpenSpec configuration and rules.
- `../../../openspec/specs/` — canonical specifications.
- `../../../openspec/changes/` — active change deltas.
- `../../../.github/workflows/openspec.yml` — CI validation workflow.