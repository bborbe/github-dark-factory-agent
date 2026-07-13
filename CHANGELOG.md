# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.1.3

- docs: mark the local-executor dependency RESOLVED in design.md and record the backend:local live-smoke findings — happy path (claude in-process, real commit, zero containers) + the HOME-sensitive claude-auth requirement the Job pod must satisfy
- feat: implement Increment 1 of the three-phase implementer — wire planning/execution/ai_review as distinct phases (domain.TaskPhase constants), route the `dark-factory-implement` task type; add RepoManager (bare-clone cache + per-task worktree), claude-auth + gh-token preflight steps, and the pure-Go planning step (clone + precondition scan: ref==PR head, PR draft, `.dark-factory.yaml` present, approved-not-completed spec in the PR diff → writes `## Plan`, routes execution; escalates without mutating assignee/status); wire `cmd/run-task` as a local per-phase harness. Execution/ai_review are stubs (Increments 2/3).
- feat: implement Increment 2 — the execution step drives the dark-factory backend:local lifecycle. Live investigation established the correct one-worktree invocation: `dark-factory daemon --set backend=local --set autoGeneratePrompts=true --auto-approve-prompts --skip-preflight --skip-healthcheck` (backgrounded → poll-until-drained → kill; one-shot `run` does NOT generate — generation is daemon-only), then `dark-factory spec complete <id>` (no auto-complete), then `git push`. Adds `pkg/dark_factory_runner.go` (`ExecutionRunner` seam over daemon/spec-complete/push) and replaces the execution stub: idempotent (`## Result` present → route ai_review), config via `--set`/flags never committed to the branch, fail-closed when a spec never reaches `verifying`/`completed` (prompt failed DoD/audit), writes typed `## Result`, routes ai_review — all without mutating assignee/status.

## v0.1.2

- chore: bump dark-factory CLI to v0.192.0 (ships spec 104 `backend: local` in-process executor) so the agent runs dark-factory without spawning nested containers (no DinD); select at runtime with `--set backend=local`

## v0.1.1

- refactor: converge build to bborbe/kafka-topic-reader publish-only model — make buca publishes docker.io/bborbe/github-dark-factory-agent:$(VERSION); deploy machinery removed.

## v0.1.0

- feat: adopt cqrs v0.6.0 / agent v0.72.0 explicit `base.TopicPrefix`; add optional `TopicPrefix` config (`env TOPIC_PREFIX`) for Kafka result topic naming — empty means unprefixed topics (Octopus per-stage clusters), non-empty preserves `develop`/`master` names (quant)
- chore: bump `github.com/bborbe/agent` v0.70.0 → v0.72.0, `github.com/bborbe/cqrs` v0.5.2 → v0.6.0
