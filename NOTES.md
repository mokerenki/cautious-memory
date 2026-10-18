# Architecture exploration notes (cautious-memory / ARTEX)

Generated before implementing eval harness, verifier, invariants, and report export.
When this brief and the code disagree, **follow the code**.

## Repo layout (actual)

| Area | Path | Notes |
|------|------|-------|
| Entry | `cmd/artex/main.go` | Binary entry |
| HTTP + lifecycle | `server/` | Manager, routes, retests, triggers, skills |
| Agents / prompts | `agent/` | planner, worker, retester prompt, tools |
| Schema | `db/schema.sql` | Embedded; re-run every startup (idempotent) |
| Findings DB | `db/findings.go`, `db/finding_retests.go`, `db/finding_traffic.go` | Standalone `findings` table |
| Skills on disk | `skills/<name>/SKILL.md` | Bound into container as `./skills` |
| Skill upload | `server/skill_zip.go`, `server/skill_usage.go` | Zip upload + usage tracking |
| Frontend | `web/src/` | Next.js; findings under `function/findings` |
| Compose | `docker-compose.yml` | postgres + artex; no eval profile yet |

There is **no** top-level `evals/` yet. Finding “retest” already exists; automatic post-create **verifier** does not.

## Agent prompts

- **Retester default prompt**: `agent/retester.go` → `RetesterDefaultPrompt` (Chinese).
- **Seed path**: `server/finding_retests.go` → `seedFindingRetester()`:
  - Seeds tools `get_finding_retest_context`, `record_finding_retest_result`
  - Inserts agent key via `db.FindingRetestAgentKey` into `agents` + `agent_prompts`
  - Idempotent via settings flag `finding_retester_seed_v1` + advisory lock
- **Planner prompt assembly**: `agent/planner.go`, `agent/promptcatalog.go`, `agent/prompt.go`
- **Worker tools**: `agent/tools.go`, `agent/toolcatalog.go`

Built-in conversation agents live in `agents` / `agent_prompts` tables, not only on-disk skills.

## Finding state machine (actual vs brief)

**Triage status** (`findings.status`, server-validated, no DB CHECK):

- `pending`, `in_progress`, `confirmed`, `resolved`, `fixed`, `false_positive`, `ignored`, `duplicate`, `risk_accepted`

Constants in `db/findings.go`. Default on insert: `pending`.

**Exploration node kind `finding`**: states `confirmed` / `dismissed` only (`db/schema.sql` `ck_node_state`).

**Retest** is a **separate** table `finding_retests` with status `pending|running|completed|failed|stopped` and verdict `reproduced|fixed|inconclusive`. Manual start from UI; not auto-triggered on finding create.

**Deviation from brief**: brief wants verification lifecycle `unverified → verifying → verified|false_positive|downgraded|needs_human` on the finding itself. Code today uses operator triage statuses. Phase 2 must extend without breaking existing triage (e.g. new columns or parallel verification_status, or carefully extend `status` + migrate old rows to `verified`/`confirmed`). Prefer additive columns if triage and verification must coexist.

## Schema location & migration pattern

- Single file: `db/schema.sql`
- Pattern: `CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`, DO blocks to recreate CHECKs when needed
- **Never** destructive DROP of data

Findings columns today: task_id, node_id, vulnclass, name, severity, summary, evidence, worker, asset_ids, status, report, evidence_version, report_evidence_version, created_at (+ traffic bindings elsewhere).

No `invariant_id`, `violation_demo`, or `blast_radius` yet.

Exploration node kinds: `begin|goal|intent|fact|finding|hint|digest` — no `invariant` yet.

## Skills embedding / shipping

- On-disk: `skills/api-recon/`, `skills/playwright-cli/`, `skills/scopesentry/` each with `SKILL.md` frontmatter (`name`, `description`)
- Runtime mount: compose volume `./skills:/app/skills`
- Upload path: zip via `skill_zip.go` (extra decompressors)
- Usage: `db/skill_usage.go`, `server/skill_usage.go`
- **Not** the same mechanism as seeded conversation agents (retester). Logic-hunter skill should ship under `skills/business-logic-hunter/`; verifier is an **agent** (like retester), not only a skill file.

## Triggers

`server/triggers.go` + `db/triggers.go`: custom agents only (`OnFinding`, interval, goal met, etc.). Builtin agents cannot use this API. Auto-verify on finding create will need engine/server wiring (similar to how retest is started), not only a DB trigger row for a builtin agent.

## Task form / goals

- Goals: `agent/goals.go`
- Task metadata / creation: server task APIs + `web/src` task creation UI
- Constraints: `task_constraints` table, injected into planner/worker prompts
- No `invariants` field on tasks yet

## Traffic & export

- Binding: `db/finding_traffic.go`, `server/finding_traffic.go`
- Export patterns: `server/finding_traffic_export.go`, `server/task_archive_package.go`, `server/task_archives.go`
- Report generation for client HTML is **not** present as described in Phase 4; existing paths are traffic/archive oriented

## UI findings

- List/detail: `web/src/app/(main)/function/findings/`
- Retest UI: `finding-retest-dialog.tsx`, `finding-retest-panel.tsx`, tasks detail retests tab
- Status badges today map triage states, not verifier conclusions

## Phase implications

1. **Phase 1 (evals/)**: pure additive under `evals/`; extend `docker-compose.yml` with `profiles: ["eval"]` — no `server/` changes.
2. **Phase 2 (verifier)**: copy `seedFindingRetester` + isolated conversation pattern; introduce verification statuses carefully relative to existing triage; hard-gate on traffic evidence; limited tools.
3. **Phase 3 (invariants)**: extend schema node kinds + finding columns; task field + planner prompt section; register `skills/business-logic-hunter`.
4. **Phase 4 (report)**: new endpoint + minimal UI; deterministic HTML only.

## Attached skill sources

- Verifier system prompt: operator-attached `verifier` skill content → embed as agent default prompt (not only disk skill).
- Business-logic-hunter: operator-attached skill → `skills/business-logic-hunter/SKILL.md`.
