---
status: approved
approved: 2026-11-11T18:12:52.925093831Z
kind: bug
---

## Summary

Sentry issue `NUKE-DEV-G9` is a real bug in `bborbe/github-dark-factory-agent` at `main.go:241`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

exitErrorForStatus (main.go:241) raises a generic error without the phase's Result.Message, so every failed phase across planning/execution/ai_review collapses into one untriageable Sentry issue; fail-closed ai_review branches (steps_ai_review.go:294 concerns / :171 PR-no-longer-draft) turn transient review errors and human-takeover races into failed tasks.

## Reproduction

The implicated source location from the deep verdict is `main.go:241` in `bborbe/github-dark-factory-agent` (resolution rule: `candidate position 1 (bborbe/github-dark-factory-agent)`), Sentry issue `NUKE-DEV-G9`.

## Recommended Fix

Pass result.Message into exitErrorForStatus and add a per-phase Sentry fingerprint so failures are self-describing and separable; do not suppress the escalation. Follow-up: retry/classify transient ai_review failures before failing closed.
