---
status: verifying
approved: "2026-11-12T13:29:27.583133189Z"
generating: "2026-11-12T13:35:48Z"
prompted: "2026-11-12T13:46:08Z"
verifying: "2026-11-12T13:46:48Z"
---

## Summary

Sentry issue `NUKE-PROD-CV` is a real bug in `bborbe/github-dark-factory-agent` at `main.go:241`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

exitErrorForStatus (main.go:241) returns an error when the agent publishes Status=failed — deliberately forcing exit 1 — but the error carries only status and phase, not result.Message, so eight distinct planning failure modes collapse into one cause-less Sentry issue.

## Reproduction

The implicated source location from the deep verdict is `main.go:241` in `bborbe/github-dark-factory-agent` (resolution rule: `candidate position 1 (bborbe/github-dark-factory-agent)`), Sentry issue `NUKE-PROD-CV`.

## Recommended Fix

In finishRun, attach result.Message (truncated), task_id, task_type and phase to ctx via errors.AddToContext before exitErrorForStatus returns, so Sentry tags carry the cause without changing grouping; optionally exclude expected business-rule planning failures via a service.OptionsFn ExcludeErrors predicate while keeping the non-zero exit.
