---
status: approved
approved: 2026-11-11T18:26:04.749966773Z
kind: bug
---

## Summary

Sentry issue `NUKE-PROD-CV` is a real bug in `bborbe/github-dark-factory-agent` at `main.go:241`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

exitErrorForStatus (main.go:241) drops result.Message and builds the error from status+phase only, so every dark-factory failure has an identical 7-frame stack and Sentry merges unrelated planning/execution/ai_review causes into one umbrella issue.

## Reproduction

The implicated source location from the deep verdict is `main.go:241` in `bborbe/github-dark-factory-agent` (resolution rule: `candidate position 1 (bborbe/github-dark-factory-agent)`), Sentry issue `NUKE-PROD-CV`.

## Recommended Fix

Include the truncated Result.Message in the exitErrorForStatus error (or wrap the sentinel with it) and fingerprint Sentry events by phase+cause prefix; keep the non-zero exit and separately decide which by-design escalations should page.
