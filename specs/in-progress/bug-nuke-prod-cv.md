---
status: verifying
approved: "2026-11-12T12:03:00.850791013Z"
generating: "2026-11-12T12:12:55Z"
prompted: "2026-11-12T12:19:41Z"
verifying: "2026-11-12T12:19:55Z"
---

## Summary

Sentry issue `NUKE-PROD-CV` is a real bug in `bborbe/github-dark-factory-agent` at `main.go:241`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

finishRun (main.go:218) passes only result.Status to exitErrorForStatus, which formats "agent reported status %q for phase %q" and discards result.Message, collapsing 8 heterogeneous planning preconditions (worktree/git fetch, PR fetch, ref mismatch, non-draft, missing .dark-factory.yaml, no approved spec) into one cause-less Sentry issue.

## Reproduction

The implicated source location from the deep verdict is `main.go:241` in `bborbe/github-dark-factory-agent` (resolution rule: `candidate position 1 (bborbe/github-dark-factory-agent)`), Sentry issue `NUKE-PROD-CV`.

## Recommended Fix

Pass the *agentlib.Result into exitErrorForStatus and include a bounded (200-char truncate) result.Message in the errors.Errorf; extend TestExitErrorForStatus to assert the message is present for failed and unchanged for exit-0 statuses.
