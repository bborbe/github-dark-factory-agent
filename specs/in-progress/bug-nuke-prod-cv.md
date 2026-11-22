---
status: approved
approved: 2026-11-22T17:14:58.57371102Z
kind: bug
---

## Summary

Sentry issue `NUKE-PROD-CV` is a real bug in `bborbe/github-dark-factory-agent` at `main.go:250`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

exitErrorForStatus (main.go:244-258) raises errors.Errorf with only status and phase, discarding result.Message, so every distinct planning/execution failure (claude auth, GH rate limit, clone, PR fetch, lifecycle) collapses into one identical non-diagnostic Sentry message.

## Reproduction

The implicated source location from the deep verdict is `main.go:250` in `bborbe/github-dark-factory-agent` (resolution rule: `candidate position 1 (bborbe/github-dark-factory-agent)`), Sentry issue `NUKE-PROD-CV`.

## Recommended Fix

Pass a bounded result.Message into the exitErrorForStatus error and fingerprint/tag by phase + coarse cause class; ship as a release since the deployed frames predate HEAD v0.8.0.
