---
name: tkt-engram-change-governance
description: "Trigger: TKT behavior changes, domain rules, authorization, contracts, decisions, bugfixes, discoveries, memory recall, or session closure. Govern TKT records with native Engram search and durable bookkeeping."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Use for TKT behavior changes, governed work, durable decisions, bugfixes, discoveries, configuration or preference changes, memory recall, and session closure. Skip formatting, CI-only work, internal test optimization, and refactors or documentation with no contractual impact.

## Hard Rules

- Scope every Engram read and write to `project: "tkt"`.
- Before relying on prior work, use `mem_context` and `mem_search` to find records by descriptive title, source path, domain, issue, or change name.
- Require a governing issue with `status:approved`. Align canonical requirements, the active delta, implementation, tests, and validation evidence.
- Save durable decisions, bugfixes, patterns, discoveries, configuration, and preferences with What, Why, Where, and Learned. Use `topic_key` only to upsert one evolving record.
- Prefer slash-separated `family/description` keys. Use `mem_suggest_topic_key` when unclear, and avoid pointer records when the complete evolving record can carry its own key.
- Preserve migration memories, but do not treat them as required routing or prerequisites. If an SDD workflow is explicitly selected, use its established `sdd/<change>/<phase>` keys.
- Engram is machine-local until a maintainer exports and imports a verified project export. CI cannot inspect it.

## Decision Gates

| Condition | Action |
| --- | --- |
| No observable impact | Skip with reason. |
| Issue missing or unapproved | Block implementation. |
| Canonical requirement or active delta is unavailable or conflicts | Clarify before behavioral work. |
| Requirements, code, tests, or validation evidence disagree | Correct or report the gap. |

## Execution Steps

1. Retrieve scoped context and search descriptive records before work that depends on prior project decisions.
2. Confirm approval, distinguish established requirements from the approved delta, and implement only that delta.
3. Save durable progress and verification with changed files, exact commands, results, alignment evidence, and unresolved gaps.
4. After compaction, persist the available summary, reload scoped context, and continue from recovered evidence.
5. Before completion, finish memory bookkeeping and write a session summary with goal, discoveries, completed work, next steps, and relevant files.

## Output Contract

Return applicability, records consulted, changed files, exact validation evidence, alignment gaps, and any blocked reason.

## References

- `AGENTS.md`: skill registration and repository workflow.
- `.agents/skills/tkt-issue-governance/SKILL.md`: issue approval rules.
- `.agents/skills/tkt-e2e/SKILL.md`: browser regression requirements.
