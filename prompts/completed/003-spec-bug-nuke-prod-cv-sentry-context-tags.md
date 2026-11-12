---
status: completed
spec: [bug-nuke-prod-cv]
summary: Attached task_id, task_type, phase, and a bounded result message as Sentry tags via errors.AddToContext in finishRun, preserving fingerprint grouping and the exit-code contract.
execution_id: 30fa0379-21ca-5a81-a728-0f468982b548-exec-003-spec-bug-nuke-prod-cv-sentry-context-tags
dark-factory-version: dev
created: "2026-11-12T13:39:43Z"
queued: "2026-11-12T13:51:42Z"
started: "2026-11-12T13:51:43Z"
completed: "2026-11-12T14:01:53Z"
---

<summary>
- A failed agent run's Sentry event now carries the cause as structured tags, not only as free text in the exception message.
- The tags are the task id, the task type, the phase, and a length-bounded form of the agent's result message.
- The tags are attached to the context that builds the non-zero-exit error, so they reach Sentry through the existing error-enrichment path with no new wiring.
- Sentry grouping is deliberately untouched: the existing fingerprint (phase + cause prefix) still decides how failures are grouped.
- The exit-code contract is unchanged: `failed` still exits non-zero; `done`, `in_progress`, and `needs_input` still exit 0.
- Long result messages stay bounded so a long rationale cannot flood the Sentry event or the pod log.
- No new configuration, environment variable, metric, or alerting/paging policy is introduced.
- Tests prove the tags are present on a failed run and that the message tag is truncated.
</summary>

<objective>
Make the non-zero-exit error for a failed agent run carry `task_id`, `task_type`, `phase`, and a bounded `result.Message` as Sentry tags by attaching them to the context with `errors.AddToContext` before `exitErrorForStatus` builds the error. Preserve the existing fingerprint grouping and the existing exit-code contract.
</objective>

<context>
Read these files in full before making any change:

- `main.go` — the relevant code is `Run` (the `finishRun` call site near the end), `finishRun`, `exitErrorForStatus`, `truncateRunes`, `statusMessageMaxRunes`, and `statusError`. Do not skim; the exact wiring below depends on the shapes that are actually there.
- `main_internal_test.go` — the plain-`testing` tests for this package, including `TestExitErrorForStatus`, `TestExitErrorForStatusTruncatesLongMessage`, `TestStatusErrorFingerprint`, and `TestSentryFingerprintProcessor`. New tests go here.
- `CHANGELOG.md` — the `## Unreleased` section is the first section; add the entry there.
- `cmd/run-task/main.go` — read only to confirm it is out of scope: it calls `agent.Run` and `agentlib.PrintResult` but never maps a status to an exit code, so it must not be changed.
- `docs/design.md` — read the phase and exit-code contract so you do not accidentally change it.

Read these guides before writing any code or tests (these live in the YOLO container, not this repo):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — wrap with `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — table-driven plain tests in `package main` are the correct form here.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — changed-behavior coverage.

Verified source facts you must use exactly (read from the working tree and the pinned modules; do not re-derive from memory):

- `finishRun(ctx context.Context, result *agentlib.Result, phase domain.TaskPhase) error` currently does `agentlib.PrintResult(ctx, result)` and then `return exitErrorForStatus(ctx, result.Status, phase, result.Message)`. It is called from exactly one place in `application.Run`: `return finishRun(ctx, result, a.Phase)`.
- `exitErrorForStatus(ctx context.Context, status agentlib.AgentStatus, phase domain.TaskPhase, message string) error` returns nil unless `status == agentlib.AgentStatusFailed`. For failed it builds `errors.Errorf(ctx, "agent reported status %q for phase %q: %s", status, phase, displayMessage)` and wraps it in `*statusError` with `fingerprint: []string{"agent-status-failed", string(phase), causePrefix(message)}`.
- `*statusError` implements `Error()`, `Unwrap()`, `Cause()`, and `Fingerprint()`. The `Cause()` method is what keeps `github.com/bborbe/errors` data traversal working through the wrapper.
- `github.com/bborbe/errors` v1.6.6 exposes `func AddToContext(ctx context.Context, key string, value any) context.Context`. It returns a NEW context that accumulates keys (it copies the previous map), so chained calls are safe.
- `github.com/bborbe/errors` v1.6.6 implements `Errorf(ctx, ...)` as `AddContextDataToError(ctx, errors.Errorf(...))`, i.e. it embeds `errors.DataFromContext(ctx)` into a `DataError` that exposes `Data()` and `Cause()`. So attaching data to the ctx passed to `exitErrorForStatus` makes that data part of the returned error chain.
- `github.com/bborbe/service` v1.11.2 `service.Run` captures the application error with `&sentry.EventHint{Context: ctx, OriginalException: err}`.
- `github.com/bborbe/sentry` v1.9.20 `addErrorTags` reads `errors.DataFromError(hint.OriginalException)` and writes every key/value into `event.Tags`. `DataFromError` walks the `Cause()` chain, so it reaches the `DataError` nested inside `*statusError`.
- `agentlib.TaskIdentifier` is a `string` type with `func (t TaskIdentifier) String() string`. `application.TaskID` is `agentlib.TaskIdentifier`; `application.TaskType` is `string`.
- `domain.TaskPhase` is a `string` type with `func (t TaskPhase) String() string`.
- `agentlib.Result` has fields `Status AgentStatus`, `NextPhase string`, `Message string`, `ContinueToNext bool`, plus turn/interaction counters.
- `agentlib.PrintResult(ctx context.Context, result *Result) error` is nil-safe and prints the result as JSON to stdout.
- `truncateRunes(s string, max int) string` and `statusMessageMaxRunes = 500` already exist in `main.go`.
- `.dark-factory.yaml` uses `workflow: direct` and does not set `hideGit: true`.

<!--
OPEN QUESTIONS FOR THE HUMAN REVIEWER (resolved best-effort from the spec; adjust at audit time if you disagree):

1. The spec's Problem statement is stale: the failed-result message and the phase+cause-prefix fingerprint were already shipped by the predecessor spec (see CHANGELOG `## Unreleased` and prompts/completed/001-fix-nuke-prod-cv-exit-error-fingerprint.md). The genuinely new, not-yet-present part of the spec's Recommended Fix is the `errors.AddToContext` tag enrichment, which is what this prompt implements. It does not re-add the message text or the fingerprint.

2. The spec says "attach result.Message (truncated)" but does not name the tag key. This prompt uses the key `message`. Change it if you prefer `result_message` or `cause`.

3. The spec says "truncated" but gives no bound. This prompt reuses the existing `statusMessageMaxRunes` (500 runes), which is already used for the error text. Note that Sentry caps tag values well below 500 characters and may flatten multi-line values; the existing `causePrefix(message)` (first line, 100 runes) is available if you want a shorter, single-line tag instead.

4. The spec's second, explicitly "optional" clause — a `service.OptionsFn` ExcludeErrors predicate for "expected business-rule planning failures" — is deliberately NOT implemented in this prompt. The spec does not enumerate which planning failures are expected business rules, and the predecessor spec names real infrastructure failures (worktree/git fetch, PR fetch) that occur in the same planning phase. A blanket planning-phase exclusion would silence those real bugs while keeping the non-zero exit, which is a regression in observability. If you want this, provide the exact message set (or a marker such as the existing `Transient() bool` convention) and it should be a follow-up prompt with its own test.

5. The `github.com/bborbe/sentry` package warns that context data becomes Sentry tags and must not contain secrets or PII. `result.Message` is already written verbatim into the error text and the pod log, so this change does not widen exposure materially; it only makes the same bounded text filterable in Sentry.
-->

</context>

<requirements>

## 1. Widen `finishRun` to receive the task identity

In `main.go`, change `finishRun` to accept the task id and task type, in addition to the result and phase. Use this exact shape:

```go
func finishRun(
	ctx context.Context,
	result *agentlib.Result,
	phase domain.TaskPhase,
	taskID agentlib.TaskIdentifier,
	taskType string,
) error {
	if err := agentlib.PrintResult(ctx, result); err != nil {
		return err
	}
	ctx = errors.AddToContext(ctx, "task_id", taskID.String())
	ctx = errors.AddToContext(ctx, "task_type", taskType)
	ctx = errors.AddToContext(ctx, "phase", phase.String())
	ctx = errors.AddToContext(ctx, "message", truncateRunes(result.Message, statusMessageMaxRunes))
	return exitErrorForStatus(ctx, result.Status, phase, result.Message)
}
```

Do not change the order or content of the two existing statements other than placing the four `AddToContext` calls between `PrintResult` and `exitErrorForStatus`. The four calls must chain (each reassigns `ctx`) so that all four keys accumulate.

## 2. Update the single call site

In `application.Run`, change the final line from `return finishRun(ctx, result, a.Phase)` to:

```go
return finishRun(ctx, result, a.Phase, a.TaskID, a.TaskType)
```

`a.TaskID` is an `agentlib.TaskIdentifier` and `a.TaskType` is a `string`; pass them directly.

## 3. Leave the exit-code and grouping behavior untouched

- Do NOT change `exitErrorForStatus`'s signature, its format string, or its fingerprint. The four tags are additive; grouping must remain `["agent-status-failed", <phase>, <cause prefix>]`.
- Do NOT change the return-nil behavior for `done`, `in_progress`, and `needs_input`.
- Do NOT change `statusError`, `fingerprinter`, `sentryFingerprintProcessor`, `truncateRunes`, `causePrefix`, `statusMessageMaxRunes`, or `causePrefixMaxRunes`.
- Do NOT attach the tags to a context that is not the one passed to `exitErrorForStatus`. The tags must be on the context used by the `errors.Errorf` call so that they end up in the error chain.

## 4. Add tests in `main_internal_test.go`

Add `fmt` to the standard-library imports and alias the errors package, because the file already imports the standard-library `errors`:

```go
berrors "github.com/bborbe/errors"
```

Add a test that drives the real `finishRun` boundary and asserts the tags are present in the returned error chain:

```go
func TestFinishRunAttachesSentryTags(t *testing.T) {
	ctx := context.Background()
	result := &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: "planning: PR #7 is not a draft",
	}

	err := finishRun(
		ctx,
		result,
		domain.TaskPhasePlanning,
		agentlib.TaskIdentifier("task-123"),
		"llm",
	)
	if err == nil {
		t.Fatal("expected a non-nil error for a failed result")
	}

	data := berrors.DataFromError(err)
	want := map[string]string{
		"task_id":   "task-123",
		"task_type": "llm",
		"phase":     "planning",
		"message":   "planning: PR #7 is not a draft",
	}
	for key, wantValue := range want {
		if got := fmt.Sprintf("%v", data[key]); got != wantValue {
			t.Fatalf("Sentry tag %q = %q, want %q", key, got, wantValue)
		}
	}
}
```

Add a second test that proves the message tag is bounded:

```go
func TestFinishRunTruncatesMessageTag(t *testing.T) {
	ctx := context.Background()
	message := strings.Repeat("a", statusMessageMaxRunes+100)
	result := &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: message,
	}

	err := finishRun(
		ctx,
		result,
		domain.TaskPhaseAIReview,
		agentlib.TaskIdentifier("task-456"),
		"llm",
	)
	if err == nil {
		t.Fatal("expected a non-nil error for a failed result")
	}

	got := fmt.Sprintf("%v", berrors.DataFromError(err)["message"])
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("message tag must be truncated with an ellipsis, got %q", got)
	}
	if strings.Contains(got, strings.Repeat("a", statusMessageMaxRunes+1)) {
		t.Fatalf("message tag was not truncated: %q", got)
	}
}
```

Do not weaken or delete the existing tests. Do not add a Ginkgo suite to `main_internal_test.go`; it is plain `testing` on purpose.

## 5. Add the changelog entry

Add a bullet at the top of the existing `## Unreleased` section of `CHANGELOG.md`, following the file's existing `- fix: **bold summary.** body` style:

```
- fix: **attach failure context to the Sentry event as tags.** Failed agent runs now carry `task_id`, `task_type`, `phase`, and a 500-rune-bounded `result.Message` on the Sentry event as tags, via `errors.AddToContext` before `exitErrorForStatus` builds the error. The fingerprint grouping and the non-zero exit for `failed` are unchanged; `needs_input` still exits 0.
```

</requirements>

<constraints>
- Keep the non-zero exit for `agentlib.AgentStatusFailed` — the executor records a phase success on exit 0 alone, so a failed run must still return a non-nil error.
- `needs_input`, `done`, and `in_progress` must still return nil from `exitErrorForStatus`. Do not change the parking behavior.
- Do NOT change the Sentry fingerprint or the grouping keys. The new tags are additive; grouping stays `["agent-status-failed", <phase>, <cause prefix>]`.
- Do NOT change `exitErrorForStatus`'s signature or its error format string.
- Do NOT add a Prometheus metric, a config knob, an env var, an opt-out flag, or a paging/alerting policy.
- Do NOT modify `cmd/run-task/main.go` — it has no status-to-exit mapping and is out of scope.
- Do NOT duplicate any helper, constant, type, or processor that already exists in `package main`. Check with a grep before adding.
- Wrap errors with `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- Existing tests must still pass. `make precommit` must pass.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run `make precommit` — must pass.

Run the focused tests directly:

```
go test -run 'TestFinishRun|TestExitErrorForStatus|TestStatusErrorFingerprint|TestSentryFingerprintProcessor' .
```

Confirm the tags are attached exactly once per key (four calls) and the call site is updated:

```
grep -n 'errors.AddToContext' main.go
grep -n 'finishRun(ctx, result, a.Phase, a.TaskID, a.TaskType)' main.go
```

Confirm `exitErrorForStatus` still returns nil for non-failed statuses and that the fingerprint array is unchanged:

```
grep -n 'agent-status-failed' main.go
grep -n 'if status != agentlib.AgentStatusFailed' main.go
```

Then read the final `main.go` and `main_internal_test.go` and confirm:

- The four `AddToContext` calls appear in `finishRun`, each reassigning `ctx`.
- The enriched `ctx` is the one passed to `exitErrorForStatus`.
- `failed` still returns a non-nil error; `done`, `in_progress`, and `needs_input` still return nil.
- The new tests pass and the existing exit-code and fingerprint tests are untouched.
</verification>
