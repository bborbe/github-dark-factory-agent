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

## Part 4.2 — ✅ Execution engine dependency (RESOLVED — shipped + live-validated)

The Phase-1 E2E surfaced a hard requirement: **dark-factory spawned nested claude-yolo containers** for the LLM steps. Since this agent's Job pod is *already* a claude-yolo container, that is DinD — disallowed (goal Non-goal).

**Prerequisite SHIPPED (2026-07-13): dark-factory `backend: local`** — a second `pkg/executor.Executor` (`localSubprocessExecutor`) that runs `claude` directly in the current process/cwd instead of `docker run`. Selected via config or `dark-factory run --set backend=local`. Git orchestration stays in the binary unchanged. Landed as spec 104, released **v0.192.0** (docs v0.192.1, scenario 024 v0.192.2). The agent's Dockerfile pins `DARK_FACTORY_VERSION=v0.192.0`.

**Live-validated 2026-07-13** with a real `dark-factory run --set`-style config (`backend: local`, `workflow: direct`, bare-remote sandbox):
- **Happy path:** a real approved prompt → claude ran **in-process (~11s), zero docker containers** → produced the change → `git commit` → prompt `completed`. This is the exact loop the execution step drives.
- **Fail path:** claude absent → fails closed (`claude not found on PATH`), no docker (scenario 024, `active`).

So the agent invokes `dark-factory run` (or per-prompt equivalents) with `--set backend=local` and lets dark-factory's Go binary do git; no nested containers, no DinD.

## Part 5 — Data contract & invariants

- Config via `--set` at runtime, NEVER committed to the PR branch (committed `.dark-factory.yaml` divergence conflicts `workflow:direct`'s `git merge origin/master`). Keep the branch current with `origin/<default>`.
- **★ claude auth is HOME-sensitive (live-smoke finding 2026-07-13).** `backend: local` runs `claude` as a subprocess inheriting the pod's env. If `claude`'s login credential is not discoverable from the process's `HOME`, every prompt fails fast with `Not logged in · Please run /login` (observed: overriding `HOME` broke auth even with `CLAUDE_CONFIG_DIR` set). **Requirement:** the Job pod MUST provision claude's login where the agent process's `HOME` resolves it — bake/mount the credential into the runtime `HOME` (claude-yolo image convention), do NOT rely on `CLAUDE_CONFIG_DIR` alone. Add a startup precondition check: run a trivial `claude --print` (or reuse dark-factory's claude probe) and fail the task early with a clear escalation if unauthed, rather than failing every prompt.
- **★ the dark-factory Claude PLUGIN must be installed, not just the CLI (E2E root cause 2026-07-13).** backend:local's generation and prompt-audit steps run the slash commands `/dark-factory:generate-prompts-for-spec` and `/dark-factory:audit-prompt` *inside* claude. If the runtime `CLAUDE_CONFIG_DIR` has the CLI binary but not the plugin, claude reports `Unknown command: /dark-factory:generate-prompts-for-spec` → **zero prompts generated → the spec resets to `approved` and the daemon idles at "nothing to do"** (silent — no error surfaces to the agent; looks like a generation-trigger bug but is really a provisioning gap). **Requirement:** the Dockerfile installs the plugin at build time (`claude plugin marketplace add bborbe/dark-factory && claude plugin install dark-factory@dark-factory`, verified with `claude plugin list | grep -q dark-factory`), mirroring github-pr-review-agent's build-time `coding` install. Keep the plugin's minor aligned with `DARK_FACTORY_VERSION`.
- **★ backend:local ignores dark-factory `config.env` (spec-104 gap).** `pkg/executor.localSubprocessExecutor.buildCommand` passes `--model` from config but never sets `cmd.Env` — so the subprocess inherits only the *ambient* process env. Unlike the docker backend (which injects `config.env` into the container), backend:local does NOT apply `env.ANTHROPIC_BASE_URL` / token from `~/.config/dark-factory/config.yaml`. **Requirement:** the Job pod sets `ANTHROPIC_BASE_URL` (model router) and the auth token as **pod env vars**, not via dark-factory `config.env`. (Follow-up: dark-factory could merge `config.env` into the local subprocess env so one config works for both backends.)
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

### Auto-approve blast radius (accepted)

The execution phase runs dark-factory with `--auto-approve-prompts`. This is the intended bookend design, not a blind approval. The flag triggers dark-factory's spec-078 audit-then-approve: each auto-generated prompt is AUDITED headlessly and approved only if the audit passes (fail-closed — a failing audit stops the spec and the agent escalates). The blast radius is bounded by four independent gates: (a) the human pre-approves the SPEC on the draft PR before the agent runs (first bookend); (b) the spec-078 fail-closed audit gate on every generated prompt; (c) this agent's ai_review diff-vs-spec sanity check before `human_review`; (d) the human's draft→ready flip on the PR (final bookend). The execution pod is single-tenant and ephemeral, so a rogue prompt cannot reach beyond the one PR branch. dark-factory has no per-prompt-id allowlist (`--auto-approve-prompt-ids` does not exist) — the audit gate IS the allowlist.

## Part 8 — Acceptance (per goal SCs)

Deployed in dev, consumes a real watcher task, walks planning→execution→ai_review in one pod, lands at `human_review` without flipping the PR; output parity with the Phase-1 prototype; idempotent under replay; per-repo concurrency 1. ✅ Prerequisite (dark-factory `backend: local`) shipped v0.192.0 and live-validated — the build is unblocked.

## Open decisions

1. ~~Execution engine: build the dark-factory local Executor first~~ — **RESOLVED**: built + shipped as `backend: local` (v0.192.0), live-validated happy + fail paths. The agent drives `dark-factory run --set backend=local`.
2. Scenario-required-or-not for verify (Part 7.2). Leaning: do NOT hard-require a scenario at emit time (spec 104 itself shipped without one, verified by unit tests + the live-smoke); instead the agent runs `spec complete` after prompts pass DoD, and `verify-spec` when a scenario exists. Revisit once the watcher emits real tasks.
