---
status: draft
---

## Summary

- Add per-caller GitHub API counters (`github_api_calls_total`, `github_api_rate_limited_total`) to `github-dark-factory-agent`, matching `github-build-watcher` v0.6.0.
- Count every REST call by `service`, `credential`, `bucket`, `outcome`, and `cluster`, deriving `bucket` from the request path.
- Count the `/rate_limit` preflight too — it uses its own `http.Client`.
- Push via the existing pushgateway at process exit; no new metrics server.

## Problem

`github-dark-factory-agent` authenticates as GitHub App `4309180` (prod) / `4290340` (dev) and makes hand-rolled REST calls, but reports nothing about how much of the GitHub quota it spends. When the shared `4309180` installation rate-limits, there is no way to attribute the burn to this caller without reading its code and logs.

## Goal

A Prometheus series `github_api_calls_total{service="github-dark-factory-agent",credential="4309180",bucket="core",outcome="success",cluster="nuke-prod"}` exists and is non-zero after a real dispatch, answering "who consumed the quota" in one query.

## Non-goals

- No new `/metrics` HTTP server (the workload is a Job that pushes at exit).
- No changes to other services' counters.

## Acceptance Criteria

- [ ] `github_api_calls_total` and `github_api_rate_limited_total` are exposed with all bounded label combinations pre-initialised to zero.
- [ ] `bucket` derives from the request path; a test proves `X-RateLimit-Resource` cannot move a call.
- [ ] The `/rate_limit` preflight is counted as `bucket="core"` via its own `http.Client`.
- [ ] `cluster` reads from the `CLUSTER` env with a code default; a test with `CLUSTER=nuke-dev` asserts the emitted label.
- [ ] No token appears in any label.
- [ ] `make precommit` passes.

## Verification

`make precommit`

## Desired Behavior

1. Every REST call through either client increments `github_api_calls_total` for its request-path bucket.
2. The `/rate_limit` preflight increments it too.
3. `github_api_rate_limited_total` pre-initialises to zero with `class="unclassified"`.
4. Counters are collected by the registry pushed at exit.

## Constraints

- Both `http.Client` instances (`pkg/github.go` `NewGitHubClient`, `pkg/steps_gh_token.go` `newGHTokenCheckStep`) must be wrapped with the same recorder bound to the same registry.
- No token in any label; `credential` is the App id.
