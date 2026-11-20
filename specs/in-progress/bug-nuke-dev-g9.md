---
status: approved
approved: 2026-11-20T01:28:49.453380543Z
kind: bug
---

## Summary

Sentry issue `NUKE-DEV-G9` is a real bug in `bborbe/github-dark-factory-agent` at `main.go:250`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

main.go:250 exitErrorForStatus raises a generic 'agent reported status "failed" for phase "planning"' error to force a non-zero Job exit but omits result.Message, discarding the only field that names the actual planning failure (worktree clone, GitHub REST, GH rate-limit preflight, claude-auth probe, or spec parse) and collapsing all distinct causes into one non-actionable issue.

## Reproduction

The implicated source location from the deep verdict is `main.go:250` in `bborbe/github-dark-factory-agent` (resolution rule: `candidate position 1 (bborbe/github-dark-factory-agent)`), Sentry issue `NUKE-DEV-G9`.

## Recommended Fix

Append result.Message to the error returned by exitErrorForStatus (main.go:250) with a regression test asserting the message is present; optionally fingerprint the event by phase/message so distinct causes do not collapse, and log the deliberate retry signal rather than capturing it as an incident.
