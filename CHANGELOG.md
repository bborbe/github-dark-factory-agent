# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- fix: inject the fleet model into the backend:local daemon. The execution step now forwards the configured model (`claudelib.ClaudeModel`, from the pod's `ANTHROPIC_MODEL`) to `dark-factory daemon` as `--set model=<model>` via `NewExecutionStep`'s new constructor arg — no `os.Getenv` inside the step. The daemon resolves its model from `--set` > `.dark-factory.yaml` `model:` > a built-in Claude default; because it does NOT read `ANTHROPIC_MODEL` for its own selection, a target repo whose `.dark-factory.yaml` omits `model:` silently ran on Sonnet (a ToU violation), which on go-skeleton PR #40 produced a prompt asserting the wrong byte count and never committed the marker. Injecting the model makes the fleet model authoritative regardless of per-repo config. Unit test asserts the flag is forwarded.

## v0.3.6

- fix: ai_review no longer flags dark-factory's own bookkeeping as out-of-scope. The read-only diff-vs-spec reviewer (`pkg/prompts/review.md`) now excludes pipeline-metadata paths — `prompts/**`, `specs/**`, and `.dark-factory.yaml` — from its scope judgment, since the lifecycle necessarily commits them on every run (generated prompts, in-progress→completed moves). Previously a correct implementation escalated to `failed` at ai_review because a spec constraint like "No other file is modified" can never hold for this pipeline (observed in cluster E2E: the marker was created correctly but the reviewer flagged the prompt/spec churn). The reviewer now judges only real implementation files against the spec. Verified via the local `cmd/run-task` loop against a clean draft PR: the same scenario now returns `pass` → `human_review`.
- chore: `go mod tidy` records `github.com/maxbrunsfeld/counterfeiter/v6` as an indirect dependency.
- chore: bump `golang.org/x/text` v0.38.0 → v0.39.0 (indirect) — clears CVE-2026-56852 (infinite loop on invalid input) flagged by trivy.

## v0.3.5

- chore: bump baked `DARK_FACTORY_VERSION` v0.192.0 → v0.192.4 (CLI + plugin). v0.192.4 fixes the `generate-prompts-for-spec` command's hardcoded `/workspace` paths (docker-mount convention) → cwd-relative, so backend:local generation writes prompts into the worktree the daemon watches instead of an empty `/workspace`. Unblocks the backend:local generation→execution lifecycle.

## v0.3.4

- fix: populate remote-tracking refs (`refs/remotes/origin/*`) in the RepoManager's bare clone. `git clone --bare` maps remote branches to `refs/heads/*`, so `origin/master` never resolved in a worktree — and dark-factory's per-prompt "sync with default branch" (`git merge origin/<default>`, run by every workflow) failed with `origin/master - not something we can merge`, stalling execution after generation. New `configureRemoteTracking` sets the standard `+refs/heads/*:refs/remotes/origin/*` refspec and fetches, on both the clone and update paths.

## v0.3.3

- fix: bake the `coding` plugin into the runtime image (Dockerfile), alongside the existing `dark-factory` plugin. dark-factory's generation step invokes the prompt-creator agent, which uses the `coding` plugin's Go guides to write prompt files; without it the fleet's MiniMax model (`MiniMax-M2.7-highspeed`) produced no prompt file ("generation produced no prompt files") and the spec never left `approved`. Local runs generate fine because the operator's `~/.claude` already has `coding`; the cluster image did not. Mirrors github-pr-review-agent's build-time `coding` install.

## v0.3.2

- fix: stream the `dark-factory` daemon's stdout/stderr to the agent's own stdout in `dark_factory_runner.startDaemon` (was `cmd.Stdout/Stderr = nil`, i.e. discarded). The backend:local lifecycle — and the claude subprocesses the daemon spawns — is now visible in `kubectl logs`. Without it a hung daemon (e.g. a claude call blocking on a no-TTY onboarding prompt) was indistinguishable from slow progress until the 30-minute lifecycle deadline. Writes to the process stdout/stderr, not a repo file, so it is not swept into the daemon's commits.

## v0.3.1

- fix: hoist `ARG CLAUDE_YOLO_IMAGE` to global scope (before the first `FROM`) in the Dockerfile. It was declared inside the build stage, so the runtime `FROM ${CLAUDE_YOLO_IMAGE}` could not interpolate it and BuildKit aborted with "base name should not be blank" — `make buca` failed before any push. CI runs `make precommit` (go build/test/lint), never `docker build`, so this latent bug surfaced only on the first real image build.
- fix: build the dark-factory CLI from the pinned clone (`go install .` in the module root) instead of `go install github.com/bborbe/dark-factory@${DARK_FACTORY_VERSION}`. dark-factory's `go.mod` carries `exclude` directives, which `go install pkg@version` refuses; building from the module root honors them (same as upstream `make install`). Reuses the clone the runtime image already makes for the plugin marketplace, so there is now a single pinned clone for both CLI and plugin. Both fixes verified with a local `make build`.

## v0.3.0

- feat: add GitHub App authentication. When `APP_ID`, `INSTALLATION_ID`, and a PEM (`PEM_KEY_FILE` or `PEM_KEY`) are all set, the agent mints a short-lived installation access token at startup (`githubapp.MintIAT`) and forwards it to every git/gh subprocess — `os.Setenv("GH_TOKEN", …)` covers the dark-factory daemon's `git push` (inherits `os.Environ()`), `gh auth setup-git` installs the git credential helper, and the token is threaded into the RepoManager, GitHub REST client, and gh-token preflight. Falls back to the raw `GH_TOKEN` input when App creds are absent so local `cmd/run-task` (with `GH_TOKEN=$(gh auth token)`) keeps working; errors clearly when neither is configured. Fixes cluster clone/push failures where the pod set no `GH_TOKEN`. Copies `pkg/githubauth` (the `gh auth setup-git` configurator) from github-pr-review-agent.

- fix: add `--set hideGit=true` to the execution step's dark-factory flags — the RepoManager runs the lifecycle in a git worktree (`.git` is a file), which trips dark-factory's spec-084 worktree safety gate; without this the daemon refuses to start. Validated in the local E2E: the daemon now starts and runs inside the worktree.
- fix: install the dark-factory Claude PLUGIN in the runtime image (Dockerfile), not just the CLI binary. The backend:local lifecycle runs `/dark-factory:generate-prompts-for-spec` and `/dark-factory:audit-prompt` *inside* claude; with only the CLI installed, claude reports `Unknown command` → zero prompts generated → the spec silently resets to `approved` and the daemon idles (the E2E root cause, 2026-07-13). Mirrors github-pr-review-agent's build-time `coding` plugin install. **Pinned** to the same `${DARK_FACTORY_VERSION}` tag as the CLI (clone that tag + add as a local marketplace, not marketplace HEAD) so the plugin minor cannot drift from the CLI minor. Confirmed locally: with the plugin installed, generation produces a valid prompt.
- docs: record two backend:local provisioning requirements in design.md Part 5 — (1) the dark-factory plugin must be installed in the runtime `CLAUDE_CONFIG_DIR`; (2) backend:local ignores dark-factory `config.env`, so the model-router `ANTHROPIC_BASE_URL` + auth token must be pod env vars (a spec-104 follow-up gap).

## v0.2.0

- feat: implement Increment 1 of the three-phase implementer — wire planning/execution/ai_review as distinct phases (domain.TaskPhase constants), route the `dark-factory-implement` task type; add RepoManager (bare-clone cache + per-task worktree), claude-auth + gh-token preflight steps, and the pure-Go planning step (clone + precondition scan: ref==PR head, PR draft, `.dark-factory.yaml` present, approved-not-completed spec in the PR diff → writes `## Plan`, routes execution; escalates without mutating assignee/status); wire `cmd/run-task` as a local per-phase harness. Execution/ai_review are stubs (Increments 2/3).
- feat: implement Increment 2 — the execution step drives the dark-factory backend:local lifecycle. Live investigation established the correct one-worktree invocation: `dark-factory daemon --set backend=local --set autoGeneratePrompts=true --auto-approve-prompts --skip-preflight --skip-healthcheck` (backgrounded → poll-until-drained → kill; one-shot `run` does NOT generate — generation is daemon-only), then `dark-factory spec complete <id>` (no auto-complete), then `git push`. Adds `pkg/dark_factory_runner.go` (`ExecutionRunner` seam over daemon/spec-complete/push) and replaces the execution stub: idempotent (`## Result` present → route ai_review), config via `--set`/flags never committed to the branch, fail-closed when a spec never reaches `verifying`/`completed` (prompt failed DoD/audit), writes typed `## Result`, routes ai_review — all without mutating assignee/status.
- feat: implement Increment 3 — the ai_review step (custom verdict-routing, read-only). A deterministic gate re-checks post-conditions (PR still draft, every matched spec completed, no prompts in-flight) and escalates (`failed`) on violation; then a read-only Claude diff-vs-spec review emits a typed `## Review` verdict. Pass → `Done, human_review` (the human's draft→ready flip is the sign-off); concerns → `failed` (escalate). NEVER runs `gh pr ready`; never clears assignee (the controller does at human_review). Completes the three-phase implementer agent logic (planning → execution → ai_review).

## v0.1.3

- docs: mark the local-executor dependency RESOLVED in design.md and record the backend:local live-smoke findings — happy path (claude in-process, real commit, zero containers) + the HOME-sensitive claude-auth requirement the Job pod must satisfy

## v0.1.2

- chore: bump dark-factory CLI to v0.192.0 (ships spec 104 `backend: local` in-process executor) so the agent runs dark-factory without spawning nested containers (no DinD); select at runtime with `--set backend=local`

## v0.1.1

- refactor: converge build to bborbe/kafka-topic-reader publish-only model — make buca publishes docker.io/bborbe/github-dark-factory-agent:$(VERSION); deploy machinery removed.

## v0.1.0

- feat: adopt cqrs v0.6.0 / agent v0.72.0 explicit `base.TopicPrefix`; add optional `TopicPrefix` config (`env TOPIC_PREFIX`) for Kafka result topic naming — empty means unprefixed topics (Octopus per-stage clusters), non-empty preserves `develop`/`master` names (quant)
- chore: bump `github.com/bborbe/agent` v0.70.0 → v0.72.0, `github.com/bborbe/cqrs` v0.5.2 → v0.6.0
