---
status: completed
spec: [stale-ref-probe]
summary: 'Added exactly one line, ''Stale-ref probe: ran.'', as line 2 of README.md and left all other files unchanged.'
execution_id: feb73ade-db68-55b3-91b3-8b7b98619930-exec-001-spec-stale-ref-probe-add-probe-line
dark-factory-version: dev
created: "2026-11-17T12:34:12Z"
queued: "2026-11-17T12:41:28Z"
started: "2026-11-17T12:41:29Z"
completed: "2026-11-17T12:50:46Z"
---

# Record that the stale-ref probe ran

<summary>
- The project's main README gains exactly one new line recording that the stale-ref probe ran.
- The recorded line is plain, human-readable text, not a hidden comment or generated artifact.
- No source code, tests, documentation pages beyond that one line, or changelog entries are added.
- No other file in the repository is touched.
- The recorded line can be found by searching the README for its exact wording.
- Existing build and test behavior is unchanged.
</summary>

<objective>
Record that the stale-ref probe ran by adding exactly one line to the project README. This gives the throwaway probe spec a concrete, verifiable change in the repository and deliberately does nothing else, so the planning step's ref/head-SHA guard can be exercised end to end against a spec that is approved-but-not-completed.
</objective>

<context>
Read the project's `README.md` first. Note that line 1 is the `# github-dark-factory-agent` heading, line 2 is a blank line, and the prose paragraph begins on line 3. The file ends with a trailing newline after the final `task/executor/` bullet.

Read `docs/dod.md` for the project's general definition of done. Its requirements for CHANGELOG entries and `docs/` updates do NOT apply to this change: the spec's Scope is exactly one added README line and its Out of Scope says "Everything else." This prompt overrides the general DoD on that point.

The spec being implemented is `specs/in-progress/stale-ref-probe.md`. Read it for the exact scope, then leave it unmodified.
</context>

<requirements>
1. Open `README.md` at the repository root.
2. Insert one new line containing exactly the text `Stale-ref probe: ran.` as the second line of the file — directly after the `# github-dark-factory-agent` heading on line 1 and immediately before the existing blank line.
3. Add exactly one line. Do not add a blank line, a heading, a list item, a code fence, or any other text alongside it.
4. Leave every other line of `README.md` byte-for-byte unchanged, including the existing blank line, the prose paragraph, the tables, the code blocks, and the trailing newline at end of file.
5. Do not modify any other file. In particular, do not modify the spec file, `CHANGELOG.md`, anything under `docs/`, any Go source, any test, or any generated file.
6. Do not commit. dark-factory handles git.

Before you finish, re-run `<verification>` and confirm every check passes; walk each bullet in `<summary>` against the resulting `README.md`.
</requirements>

<constraints>
- Scope is exactly one added line in `README.md`; nothing else (copied from the spec's Scope and Out of Scope: "Add a single line to `README.md` recording that this probe ran. Nothing else." / "Everything else.").
- Do NOT add a CHANGELOG entry, a docs page, a test, or any source change — the spec explicitly scopes these out.
- Do NOT commit — dark-factory handles git.
- Do NOT modify the spec file `specs/in-progress/stale-ref-probe.md`.
- Existing tests must still pass; this change is documentation-only and must not affect them.
- The inserted line's wording must match exactly, including capitalization and the trailing period, so the verification check is deterministic.
</constraints>

<verification>
Run from the repository root:

```bash
grep -Fqx 'Stale-ref probe: ran.' README.md
[ "$(sed -n '2p' README.md)" = "Stale-ref probe: ran." ]
[ "$(grep -Fxc 'Stale-ref probe: ran.' README.md || true)" = "1" ]
```

All three commands must succeed. The first confirms the exact line exists; the second confirms it is the second line of the file; the third confirms it appears exactly once.

This change is markdown-only, so the full `make precommit` suite is unaffected and is covered separately by the project's definition-of-done validation. The targeted checks above are this prompt's verification.
</verification>

<!--
OPEN QUESTIONS FOR THE AUDITOR / HUMAN REVIEWER:
1. The spec says "add a single line" without naming its wording or placement. This prompt chooses the visible plain-text line "Stale-ref probe: ran." placed as line 2 (immediately under the H1), because appending at end-of-file would either join the final markdown list item as a lazy continuation or require a second (blank separator) line, violating "single line". If a hidden HTML comment marker or a different placement is preferred, adjust requirement 2 and the three verification checks together.
2. The general project DoD asks for a CHANGELOG entry and docs updates. This prompt deliberately omits both because the spec's Scope and Out of Scope sections override the general DoD. Confirm that is the intended reading.
-->
