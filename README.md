# github-dark-factory-agent

The cluster **dark-factory implementer**. A human drafts + approves a spec on a
draft-PR branch and walks away; this agent clones the branch and walks the
dark-factory lifecycle (planning → execution → ai_review), landing the task at
`phase: human_review` **without flipping the PR**. The human verifies + flips
draft→ready → the existing `github-pr-review-agent` merges.

Three distinct phases (see `docs/design.md`):

- **planning** — pure-Go: clone the draft-PR branch, validate preconditions
  (ref == PR head, `.dark-factory.yaml` present, an approved-not-completed spec
  in the PR diff, PR is a draft), write `## Plan`.
- **execution** — drive the dark-factory lifecycle with `backend: local`
  (Increment 2).
- **ai_review** — read-only Claude verifier → `## Review`, route to
  `human_review` (Increment 3).

## How It Works

1. Agent pipeline ([[task/controller]] → Kafka → [[task/executor]]) spawns a K8s Job with the `github-dark-factory-agent` image.
2. The Job receives `TASK_CONTENT`, `TASK_ID`, `BRANCH`, `ALLOWED_TOOLS`, `MODEL`, etc. via env vars.
3. `main.go` assembles the prompt via `lib/claude` (embedded `workflow.md` + `output-format.md` + task content).
4. Runs `claude --print --output-format stream-json` with the allowed tools.
5. Parses the JSON result and publishes to Kafka via `lib/delivery.KafkaResultDeliverer` (when `TASK_ID` set), or falls back to `NoopResultDeliverer` for local runs.

## Env Vars

| Var | Required | Default | Purpose |
|---|---|---|---|
| `TASK_CONTENT` | yes | — | Raw task markdown |
| `BRANCH` | yes | — | `dev`/`prod` — used as Kafka topic prefix |
| `TASK_ID` | no | — | Required when publishing results via Kafka |
| `MODEL` | no | `sonnet` | `sonnet` or `opus` |
| `ALLOWED_TOOLS` | no | — | Comma-separated Claude tool allowlist (e.g. `Read,Grep,Bash`) |
| `AGENT_DIR` | no | `agent` | Directory containing `.claude/CLAUDE.md` guardrails |
| `CLAUDE_CONFIG_DIR` | no | — | Claude Code OAuth config directory (PVC mount) |
| `ENV_CONTEXT` | no | — | Comma-separated `KEY=VAL` pairs injected into the prompt |
| `CLAUDE_ENV` | no | — | Comma-separated `KEY=VAL` pairs passed to the Claude CLI subprocess |
| `KAFKA_BROKERS` | no | — | Required when `TASK_ID` is set |
| `SENTRY_DSN` | no | — | Error reporting |

## Creating a New Agent

To add a domain-specific agent that reuses this binary:

1. Create a task file in OpenClaw vault with `assignee: claude-agent` (or a new assignee routed to this image via a Config CRD).
2. Mount a PVC or Secret containing the domain-specific `.claude/CLAUDE.md` and any API credentials.
3. Set `ALLOWED_TOOLS` on the Config CRD to the minimum tools the agent needs.
4. Set `ENV_CONTEXT` to inject domain context (e.g. API URLs) into the prompt without modifying the binary.

### Config CRD env pattern

The `Config` CRD's `spec.env` map becomes pod env vars, which `main.go` consumes via struct tags. Example from `k8s/github-dark-factory-agent.yaml`:

```yaml
spec:
  env:
    ALLOWED_TOOLS: WebSearch,WebFetch,Read,Grep
```

Tune `ALLOWED_TOOLS` per task shape (minimum viable set):

| Task shape | Minimum tools |
|---|---|
| Web research | `WebSearch,WebFetch,Read,Grep` |
| Vault I/O via scripts | `Bash(scripts/vault-read.sh:*),Bash(scripts/vault-write.sh:*),Bash(scripts/vault-list.sh:*),Grep` |
| API query via script | `Bash(scripts/trading-api-read.sh:*),Grep` |
| Code edit | `Read,Write,Edit,Grep,Glob,Bash(go:*),Bash(make:*)` |

Prefer constrained `Bash(path:*)` forms over bare `Bash` to minimize shell attack surface.

### Claude subprocess env allowlist

`lib/claude/claude-runner.go` strips pod env down to a safe allowlist (`HOME,PATH,USER,TZ,...`) before spawning `claude`. Custom env vars (API URLs, credentials) **must** be threaded explicitly via `ClaudeRunnerConfig.Env map[string]string` in `main.go`. Don't expect pod env to reach Claude by default. See `docs/` for precedent (trade-analysis commit `1ccfa674cf`).

## Local Quick Test

```bash
cd ~/Documents/workspaces/agent/agent/claude
go run . \
  --task-content "$(cat /path/to/task.md)" \
  --model sonnet \
  --allowed-tools "Read,Write,Edit,Bash,Grep,Glob" \
  --agent-dir agent \
  --branch dev
```

Skips K8s, task controller, task executor, git writeback. Useful for iterating on prompts.

## Links

Admin endpoints:
- Dev: <https://dev.quant.benjamin-borbe.de/admin/github-dark-factory-agent/setloglevel/3>
- Prod: <https://prod.quant.benjamin-borbe.de/admin/github-dark-factory-agent/setloglevel/3>

## Related

- `pkg/prompts/` — embedded prompts (`workflow.md`, `output-format.md`)
- `agent/.claude/CLAUDE.md` — default agent guardrails
- `docs/claude-oauth-setup.md` — seed PVC with Claude Code OAuth credentials
- `lib/claude/` — shared prompt assembly + Claude CLI invocation
- `lib/delivery/` — shared Kafka result publishing
- `task/controller/` — Obsidian→Kafka event source
- `task/executor/` — Kafka→K8s Job spawner
