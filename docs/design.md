# github-dark-factory-agent — design.md

Design artifact for the Phase-2 gate of [[Lift Dark-Factory Daemon into Agent Framework]]. Produced via `/agent:launch-agent` (which walks the [[Agent Design Guide]] interview) on 2026-07-12, drawing on the Phase-1 prototype E2E (both slash-command prototypes validated on `bborbe/go-skeleton` PR #37 through merge).

## Part 1 — Motivation

- **Problem:** dark-factory's spec→prompt→execute lifecycle runs locally (`/dark-factory:daemon` / `run`), tying autonomous coding to one author + one awake laptop.
- **Do-nothing cost:** throughput capped at one author × one machine; no laptop-free execution.
- **This agent:** the cluster implementer. A human drafts + approves a spec on a draft-PR branch and walks away; this agent implements it and lands the task at `human_review`; the human verifies + flips the PR to ready; the existing `github-pr-review-agent` reviews + merges. Two human bookends, autonomous middle.

## Part 2 — Identity

- **Name / repo:** `github-dark-factory-agent` → `github.com/bborbe/github-dark-factory-agent` (matches `github-pr-review-agent`, `github-releaser-agent` post-split convention). Image `docker.io/bborbe/github-dark-factory-agent`.
- **Assignee (Config CRD `spec.assignee`):** `github-dark-factory-agent` — MUST match the `assignee` the watcher emits.
- **Pattern:** B (k8s Job per task). **Shape:** claude (mirrors `github-pr-review-agent`).
- **Runtime:** the Job pod IS a claude-yolo container (claude + git + gh + dark-factory CLI baked in).

## Part 3 — Integration

- **Trigger:** `github-dark-factory-watcher` (Phase-1: `/github-dark-factory-watcher` slash command; Phase-3: Go watcher). Emits when a draft PR has `.dark-factory.yaml` + ≥1 `approved`-not-`completed` spec **that appears in the PR's own diff** (diff-scoping — validated fix; "spec exists on branch" alone false-triggers on repos that persist specs).
- **Task contract:** [[Agent Task File Contract]] — `task_type: dark-factory-implement`, `assignee: github-dark-factory-agent`, `repo`/`clone_url`/`ref`/`pr_number`/`branch`; body is an operator-readable header, the clone is the data source.
- **Downstream:** lands task at `phase: human_review` (never flips the PR). Human flips draft→ready → `github-pr-watcher` → `github-pr-review-agent`.

## Part 4 — Behavior (phases)

| Phase | Impl shape | Reads | Side effects | Writes |
|---|---|---|---|---|
| planning | pure-Go spec scan (`agent/code` shape) | frontmatter (`clone_url`,`ref`,`branch`,`pr_number`) | clone branch, precondition checks (ref==HEAD, `.dark-factory.yaml`, approved-not-completed spec, PR draft) | `## Plan` |
| execution | custom step (dark-factory lifecycle + git) | `## Plan` | generate prompts → audit+approve → execute → verify → `spec complete`; commit + push per prompt | `## Result` |
| ai_review | `claudelib.NewAgentStep` (read-only) | `## Plan`+`## Result` | remote checks: all specs `completed`, no prompts in-flight, PR still draft, diff-vs-spec sanity | `## Review`; `phase: human_review`, clear assignee |

**Never runs `gh pr ready`.** The human's flip is the verification sign-off.

## Part 4.2 — ★ Execution engine dependency (blocks the build)

The Phase-1 E2E proved the pipeline works but surfaced a hard architectural requirement: **dark-factory today spawns nested claude-yolo containers** for the two LLM steps (spec→prompts, prompt→code); git orchestration (branch/merge/commit/push) runs in the Go binary. Since this agent's Job pod is *already* a claude-yolo container, spawning nested containers is DinD — disallowed (goal Non-goal).

**Required dark-factory feature (prerequisite, not yet built):** a **local/in-process Executor** — a second implementation of `dark-factory/pkg/executor.Executor` (today only `dockerExecutor`) that runs `claude` directly in the current process/cwd instead of `docker run`, selected via `--set executor=local` (or config). Git orchestration stays in the binary unchanged. See the "Make Dark-Factory Executor Interface Backend-Neutral" work — the seam exists.

Until that ships, this agent CANNOT be built cleanly. The Phase-1 E2E ran dark-factory with `--set hideGit=true` and let it spawn containers on the laptop — acceptable for the prototype, not for the cluster Job.

## Part 5 — Data contract & invariants

- Config via `--set` at runtime, NEVER committed to the PR branch (committed `.dark-factory.yaml` divergence conflicts `workflow:direct`'s `git merge origin/master`). Keep the branch current with `origin/<default>`.
- Agent MUST drive `dark-factory spec complete` (else spec stays `verifying` = approved-not-completed → watcher re-emits).
- Idempotency: `AgentStep.ShouldRun` checks if the phase's `##` section exists; per-prompt commits make execution crash-resumable.
- Concurrency: per-repo cap 1 (Config CRD) — two PRs on one repo must not race git push.

## Part 6 — Operations

- Pattern B Job (not cron); resource profile ~ `github-pr-review-agent`.
- Kill switch: remove/disable the Config CRD.
- Observability: phase-transition logs + metrics; per-prompt execution visible.

## Part 7 — Safety / failure modes

1. **Prompt fails DoD / audit fail** — spec-078 fail-closed stops the spec; agent escalates (clear assignee, `previous_assignee`, phase unchanged). NO auto-fix loop.
2. **Spec has no scenario** — `verify-spec` can't pass; either require a scenario at watcher-emit time, or `spec complete` after prompts (less safety). Decide at build time.
3. **`workflow:direct` git-merge-origin/master conflict** — mitigated by config-via-`--set` + non-divergent branch + pre-run sync.
4. Security: GH token + git-push creds via Secret; PR content visible to claude (acceptable, zero-retention).

## Part 8 — Acceptance (per goal SCs)

Deployed in dev, consumes a real watcher task, walks planning→execution→ai_review in one pod, lands at `human_review` without flipping the PR; output parity with the Phase-1 prototype; idempotent under replay; per-repo concurrency 1. Gated on the local-Executor dark-factory feature.

## Open decisions

1. Execution engine: build the dark-factory **local Executor** feature first (prerequisite) vs. interim wrapper. Recommend: build the feature — it's the clean foundation and the seam exists.
2. Scenario-required-or-not for verify (Part 7.2).
