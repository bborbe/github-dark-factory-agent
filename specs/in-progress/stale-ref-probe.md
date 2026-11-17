---
status: prompted
approved: "true"
generating: "2026-11-17T12:33:06Z"
prompted: "2026-11-17T12:37:45Z"
---

# Stale-Ref Probe

Throwaway spec used to exercise the planning step's ref/head-sha guard end to end.

## Problem

A record emitted at one PR head SHA is re-attempted after the branch advances, so the planning step's guard fires on every attempt. This spec exists only so the guard has an approved-not-completed spec to find in the PR diff; no implementation is wanted.

## Scope

Add a single line to `README.md` recording that this probe ran. Nothing else.

## Success Criteria

- [ ] `README.md` carries the probe line.

## Out of Scope

Everything else.
