# Definition of Done

The validation prompt dark-factory runs against each executed prompt before it
marks the prompt complete. A change is Done only when every box holds.

- [ ] Builds: `make precommit` exits 0 (generate + format + test + lint + vet + gosec + coverage).
- [ ] Tests: new/changed behavior has Ginkgo coverage; boundaries are mocked (counterfeiter), the framework Agent is not.
- [ ] Scope: the diff touches only what the spec asked for; no unrelated files.
- [ ] Errors: wrapped via `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- [ ] Escalation doctrine: failure paths return `Status: failed`/`needs_input` without mutating `assignee`/`status` or writing a `## Failure` section.
- [ ] Docs: user-facing behavior changes are reflected in `docs/` and the CHANGELOG `## Unreleased` section.
