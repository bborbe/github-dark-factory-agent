# Creating Claude-Based Agents

Guide for building new domain-specific agents on top of `agent/claude` as a copy-paste template.

`agent/claude` is a **reference implementation** — a generic Pattern B agent (K8s Job spawned by `task/executor`) that wraps Claude Code CLI. It ships minimal so domain agents can fork it and swap:

1. `.claude/CLAUDE.md` guardrails
2. `pkg/prompts/` (workflow + output format)
3. `ALLOWED_TOOLS` env
4. `scripts/` (safe shell APIs for domain I/O)
5. `main.go` struct fields for domain env (API URLs, credentials)

No changes to `lib/claude` or `lib/delivery` required.

## Related Docs

- [[agent-crd-specification.md]] — Config CRD fields
- [[agent-job-interface.md]] — Pattern B contract (env vars, Kafka, exit codes)
- [[agent-job-lifecycle.md]] — full task → job → result flow
- `agent/claude/README.md` — template agent README

---

## Architecture Recap

```
Obsidian task → task/controller → Kafka (agent-task-v1-event)
                                     ↓
                                  task/executor
                                     ↓ spawns K8s Job
                              agent-<domain> (your binary)
                                     ↓ spawns subprocess
                                  claude --print (Claude Code CLI)
                                     ↓ uses
                              scripts/*.sh + WebFetch + ...
                                     ↓
                         JSON result → lib/delivery → Kafka (agent-task-v1-request)
                                     ↓
                                task/controller → git writeback
```

Your agent is the thin wrapper between the task pipeline and Claude Code. It:
1. Receives `TASK_CONTENT` from env
2. Assembles a prompt via `lib/claude` (embedded workflow.md + output-format.md + your domain prompts)
3. Spawns `claude --print --allowedTools=...` in a subprocess with a restricted env
4. Parses the stdout JSON result
5. Publishes it to Kafka via `lib/delivery.KafkaResultDeliverer`

---

## Step-by-Step: New Claude Agent

### 1. Copy the template

```bash
cp -r ~/Documents/workspaces/agent/agent/claude \
      ~/Documents/workspaces/<your-repo>/agent/<domain>
cd ~/Documents/workspaces/<your-repo>/agent/<domain>
```

Rename module in `go.mod`, update imports in `main.go`, `pkg/`.

### 2. Decide the tool surface

Pick the **minimum** `ALLOWED_TOOLS` for your domain. Use built-in Claude tools and/or constrained Bash scripts. Prefer scripts over bare `Bash`.

| Task shape | Typical tools |
|---|---|
| Web research | `WebSearch,WebFetch,Read,Grep` |
| Vault I/O via git-rest | `Bash(scripts/vault-read.sh:*),Bash(scripts/vault-write.sh:*),Bash(scripts/vault-list.sh:*),Grep` |
| API query | `Bash(scripts/api-read.sh:*),Grep` |
| Code edits | `Read,Write,Edit,Grep,Glob,Bash(go:*),Bash(make:*)` |
| Hybrid (vault + API) | combine vault-*.sh + api-*.sh + `Grep` |

**Rules of thumb:**
- Never grant bare `Bash` unless the task truly needs arbitrary shell
- Wrap every external I/O in a script under `agent/scripts/` so the allowlist stays `Bash(scripts/<name>.sh:*)`
- Exclude `Write` unless agent must produce a file (most agents return JSON via stdout)

### 3. Write domain scripts

`agent/scripts/*.sh` are your API surface to Claude. Keep them:
- Pure I/O — one purpose per script
- `set -e -o pipefail`
- Fail loudly if env vars missing (`: "${VAR:?VAR is required}"`)
- Treat stdout as the result, stderr for diagnostics

Precedent: `trading/agent/trade-analysis/agent/scripts/`:
- `trading-api-read.sh` — HTTP GET to trading API with optional basic auth
- `vault-read.sh`, `vault-write.sh`, `vault-list.sh` — curl to git-rest HTTP service

### 4. Write domain prompts

Replace `pkg/prompts/workflow.md` with your domain workflow. Keep `output-format.md` standard (JSON `{status, message, ...}`) — `lib/delivery` expects it.

Use `agent/.claude/CLAUDE.md` for hard guardrails (no internal network, no secret exfiltration, no shell escapes, forbidden paths). Agent-claude's default CLAUDE.md is a good baseline — keep the Forbidden/Output/Tools/Data sections.

### 5. Thread domain env through main.go

**CRITICAL:** `lib/claude/claude-runner.go` strips pod env to a safe allowlist (`HOME,PATH,USER,TZ,ZONEINFO,TMPDIR,LANG,LC_ALL`). Any custom env your scripts need (API URLs, credentials) must be explicitly forwarded via `ClaudeRunnerConfig.Env`:

```go
type application struct {
    // ... standard fields ...
    TradingAPIURL      string `required:"true"  arg:"trading-api-url"      env:"TRADING_API_URL"`
    TradingAPIUsername string `required:"false" arg:"trading-api-username" env:"TRADING_API_USERNAME" display:"length"`
    TradingAPIPassword string `required:"false" arg:"trading-api-password" env:"TRADING_API_PASSWORD" display:"length"`
}

func (a *application) Run(ctx context.Context, sentryClient libsentry.Client) error {
    taskRunner := factory.CreateTaskRunner(
        a.ClaudeConfigDir, a.AgentDir,
        claudelib.AllowedTools{
            "Grep",
            "Bash(scripts/trading-api-read.sh:*)",
        },
        claudelib.SonnetClaudeModel,
        map[string]string{
            "TRADING_API_URL":      a.TradingAPIURL,
            "TRADING_API_USERNAME": a.TradingAPIUsername,
            "TRADING_API_PASSWORD": a.TradingAPIPassword,
        },
        // ...
    )
    // ...
}
```

**Symptom of forgetting:** script fails with `X is required` even though `kubectl describe pod` shows the var is set. The pod has it — the Claude subprocess doesn't.

**Precedent:** `trading/agent/trade-analysis` commit `1ccfa674cf` fixed exactly this.

Use `display:"length"` on credential fields so startup logs show length only, not the secret.

### 6. K8s manifests

Three files under `agent/<domain>/k8s/`:

**`agent-<domain>.yaml`** — the Config CRD. `spec.env` becomes pod env:

```yaml
apiVersion: agent.benjamin-borbe.de/v1
kind: Config
metadata:
  name: agent-<domain>
  namespace: '{{ "NAMESPACE" | env }}'
spec:
  assignee: <domain>-agent           # matches task assignee
  image: docker.quant.benjamin-borbe.de:443/agent-<domain>
  heartbeat: 5m
  secretName: agent-<domain>
  volumeClaim: agent-<domain>         # PVC with Claude Code OAuth config
  volumeMountPath: /home/claude/.claude
  env:
    ALLOWED_TOOLS: "Grep,Bash(scripts/trading-api-read.sh:*)"
    TRADING_API_URL: http://frontend-gateway:9090
  resources:
    requests: {cpu: 500m, memory: 1Gi, ephemeral-storage: 2Gi}
    limits:   {cpu: 500m, memory: 1Gi, ephemeral-storage: 2Gi}
```

**`agent-<domain>-secret.yaml`** — credentials via teamvault templating:

```yaml
apiVersion: v1
kind: Secret
type: Opaque
metadata:
  name: agent-<domain>
  namespace: '{{ "NAMESPACE" | env }}'
data:
  SENTRY_DSN:          '{{ "SENTRY_DSN_KEY"         | env | teamvaultUrl      | base64 }}'
  TRADING_API_USERNAME:'{{ "FRONTEND_GATEWAY_API_KEY"| env | teamvaultUser     | base64 }}'
  TRADING_API_PASSWORD:'{{ "FRONTEND_GATEWAY_API_KEY"| env | teamvaultPassword | base64 }}'
```

**`agent-<domain>-pvc.yaml`** — PVC for Claude Code OAuth config (shared across runs, seeded once per `docs/claude-oauth-setup.md`).

### 7. Build + deploy

Makefile should include the standard BUCA chain (build → upload → commit → apply). Copy from `trading/agent/trade-analysis/Makefile`.

```bash
cd agent/<domain>
BRANCH=dev make buca
```

### 8. Register assignee → agent mapping

Ensure `task/executor` CRDs are synced so a task with `assignee: <domain>-agent` routes to your image. The Config CRD handles this automatically once applied.

### 9. Smoke test

Create a minimal task in OpenClaw vault:

```markdown
---
assignee: <domain>-agent
status: in_progress
phase: in_progress
task_identifier: <uuid>
---
Tags: [[Task]]

---

<minimal domain task description>
```

Verify end-to-end:
1. `kubectlquant -n dev get job -w` — job spawned
2. `kubectlquant -n dev logs job/<name>` — agent started, args printed include your domain fields
3. Task result appears in vault after agent exits

**Common pitfalls:**
- Task content pollution from retries — create fresh task with new UUID between runs
- Image cache — confirm `:dev` image digest changed after `make buca`
- Allowlist gaps — check Claude's tool invocations in logs, grant missing tools explicitly

---

## Minimal Files Changed vs Template

When forking `agent/claude`:

| File | Change |
|---|---|
| `go.mod` | new module path |
| `main.go` | add domain env fields, thread into `ClaudeRunnerConfig.Env` |
| `pkg/prompts/workflow.md` | domain workflow |
| `pkg/prompts/output-format.md` | usually unchanged |
| `agent/.claude/CLAUDE.md` | domain guardrails |
| `agent/scripts/*.sh` | domain shell APIs |
| `k8s/agent-<domain>.yaml` | new CRD with `spec.env` |
| `k8s/agent-<domain>-secret.yaml` | teamvault wiring |
| `k8s/agent-<domain>-pvc.yaml` | PVC for OAuth config |
| `Dockerfile`, `Makefile` | rename image + module |
| `lib/claude`, `lib/delivery` | **never** — shared |

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Script: `X is required` despite pod env set | `lib/claude` env allowlist | thread X via `ClaudeRunnerConfig.Env` |
| Job respawns after success | `current_job` not cleared by controller | known executor bug, separate fix |
| Claude refuses tool use | `ALLOWED_TOOLS` missing | add to CRD `spec.env`, redeploy |
| Task result stays "in_progress" | agent crashed before Kafka publish | check `kubectlquant logs` for panic |
| Image not updated after `make buca` | wrong branch or cache | `BRANCH=dev make buca` from correct worktree, confirm digest in registry |

---

## References

- `lib/claude/claude-runner.go:93` — env allowlist source
- `lib/claude/allowed-tools.go` — tool allowlist parser
- `task/executor/pkg/spawner/job_spawner.go` — CRD `spec.env` → pod env
- `trading/agent/trade-analysis/` — complete Claude-based agent example
