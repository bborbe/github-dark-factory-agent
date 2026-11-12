---
status: completed
spec: [bug-nuke-prod-cv]
summary: Verified the predecessor NUKE-PROD-CV fix is fully present in main.go, main_internal_test.go, and CHANGELOG.md (message forwarding, bounded 500-rune inclusion, nil for done/in_progress/needs_input, fingerprint processor, and failed-message test); made no code changes to avoid duplicating an existing fix, and make precommit passed.
execution_id: 7511383e-a694-5f95-ae19-740904509a5c-exec-002-spec-bug-nuke-prod-cv-exit-error-message
dark-factory-version: dev
created: "2026-11-12T12:20:00Z"
queued: "2026-11-12T12:23:53Z"
started: "2026-11-12T12:23:54Z"
completed: "2026-11-12T12:34:26Z"
---

<summary>
- A failed agent run must fail the Job with an error that names *why* it failed, not just that it failed.
- The prior fix for this bug already lives in this repo: the non-zero-exit error now carries the failed result's human-readable message.
- This prompt first checks whether that fix is still present, and if it is, makes no code change at all — it will not add a second copy of the same helpers.
- If any part of the fix is somehow missing (for example after a revert), it adds only the missing piece.
- The exit-code contract is preserved: `failed` still exits non-zero; `done`, `in_progress`, and `needs_input` still exit 0.
- A test must prove the failed run's error contains the result message.
- Long messages stay bounded so a long rationale cannot flood the pod log.
- No new configuration, environment variable, metric, or alerting/paging policy is introduced.
</summary>

<objective>
Ensure that the non-zero-exit error produced for a failed agent run includes a bounded form of `agentlib.Result.Message`, per the `NUKE-PROD-CV` spec. The fix is expected to already be present from the completed predecessor spec; this prompt reconciles the current tree against the spec and adds only what is missing, never a duplicate.
</objective>

<context>
Read these files in full before making any change:

- `main.go` — the relevant code is `finishRun`, `exitErrorForStatus`, `truncateRunes`, `causePrefix`, `statusError`, and `sentryFingerprintProcessor`. Do not skim; the whole decision below depends on what is actually there.
- `main_internal_test.go` — the plain-`testing` tests for this package, including `TestExitErrorForStatus`.
- `specs/completed/bug-nuke-prod-cv.md` — the predecessor spec that this re-filed spec duplicates. Read it to see the original intent.
- `prompts/completed/001-fix-nuke-prod-cv-exit-error-fingerprint.md` — the completed implementation prompt that already delivered this fix.
- `CHANGELOG.md` — check the `## Unreleased` section; the entry for this fix is already expected to be there.
- `cmd/run-task/main.go` — read only to confirm it is out of scope: it calls `agent.Run` and `agentlib.PrintResult` but never maps a status to an exit code, so it must not be changed.

Read these guides before writing any code or tests (these live in the YOLO container, not in this repo):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — wrap with `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — table-driven plain tests in `package main` are the correct form here.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — changed-behavior coverage.

Verified current source facts (read from the working tree — do not re-derive from memory):

- `finishRun(ctx context.Context, result *agentlib.Result, phase domain.TaskPhase) error` already ends with `return exitErrorForStatus(ctx, result.Status, phase, result.Message)`.
- `exitErrorForStatus(ctx context.Context, status agentlib.AgentStatus, phase domain.TaskPhase, message string) error` already includes the message: it calls `truncateRunes(message, statusMessageMaxRunes)` and formats `"agent reported status %q for phase %q: %s"`, substituting `"(no message)"` when empty. It returns nil for every status other than `agentlib.AgentStatusFailed`.
- `statusMessageMaxRunes = 500` and `causePrefixMaxRunes = 100` already exist as constants; `truncateRunes` and `causePrefix` already exist; `statusError` (with `Fingerprint()`, `Error()`, `Unwrap()`, `Cause()`) and `sentryFingerprintProcessor` already exist and are registered in `main`.
- `main_internal_test.go` already has `TestExitErrorForStatus` (table with a `message` field and an assertion that the failed error contains the message), `TestExitErrorForStatusTruncatesLongMessage`, `TestStatusErrorFingerprint`, and `TestSentryFingerprintProcessor`.
- `CHANGELOG.md` already has an `## Unreleased` entry describing this fix.

<!--
OPEN QUESTIONS FOR THE HUMAN REVIEWER (resolved best-effort from the spec; adjust at audit time if you disagree):

1. This spec re-files a bug that is already fixed. The predecessor spec (specs/completed/bug-nuke-prod-cv.md) and its completed prompt (prompts/completed/001-fix-nuke-prod-cv-exit-error-fingerprint.md) already delivered the change, and the working tree matches it. The likely cause is that the sentry-fix-agent re-detected NUKE-PROD-CV from events captured before the fix shipped. This prompt is therefore a safe reconcile: it changes nothing when the fix is present.
2. The spec's "Recommended Fix" says "bounded (200-char truncate)". The shipped code bounds at 500 runes (statusMessageMaxRunes). This prompt deliberately keeps 500 runes: it is already bounded, rune-aware, and covered by a test. Downgrading to 200 would be churn with no behavior the spec actually needs. Change the constant only if you specifically want 200.
3. The spec says "Pass the *agentlib.Result into exitErrorForStatus". The shipped signature takes `message string` instead. That is functionally equivalent, avoids widening the function's dependencies, and is already tested. This prompt does not change the signature.
4. The shipped fix also adds a Sentry fingerprint (phase + bounded cause prefix) so heterogeneous failures no longer merge into one issue. That is beyond this spec's literal text but is the actual fix for the "one cause-less Sentry issue" symptom. It is kept; do not remove it.
-->

</context>

<requirements>

## 1. Establish the current state before editing

Read `main.go` and `main_internal_test.go` and confirm the following five facts:

1. `finishRun` passes `result.Message` as the fourth argument to `exitErrorForStatus`.
2. `exitErrorForStatus` accepts `message string` and includes the bounded message in its `errors.Errorf` format string.
3. `exitErrorForStatus` returns nil for every status other than `agentlib.AgentStatusFailed`.
4. `TestExitErrorForStatus` passes a non-empty `message` for the failed case and asserts `strings.Contains(err.Error(), c.message)`.
5. `CHANGELOG.md` has an `## Unreleased` entry for this fix.

## 2. Decision gate — do not duplicate an existing fix

If all five facts above hold (this is the expected state), the fix is already present. In that case:

- Make **no functional change** to `main.go` or `main_internal_test.go`.
- Do **not** add a second declaration of `truncateRunes`, `causePrefix`, `statusMessageMaxRunes`, `causePrefixMaxRunes`, `statusError`, `fingerprinter`, or `sentryFingerprintProcessor`. Duplicate declarations in the same package do not compile.
- Do **not** change the bound from `500` runes to `200`.
- Do **not** change `exitErrorForStatus` to take `*agentlib.Result`.
- Do **not** rewrite `CHANGELOG.md`; the entry is already correct.

Proceed to step 4 (verification only).

## 3. If (and only if) a fact from step 1 is missing

Add exactly the missing piece, matching the shapes below and the surrounding style. Do not add any piece that is already present.

### 3a. `finishRun` must forward the message

```go
return exitErrorForStatus(ctx, result.Status, phase, result.Message)
```

### 3b. `exitErrorForStatus` must include a bounded message

```go
func exitErrorForStatus(
	ctx context.Context,
	status agentlib.AgentStatus,
	phase domain.TaskPhase,
	message string,
) error {
	if status != agentlib.AgentStatusFailed {
		return nil
	}
	displayMessage := truncateRunes(message, statusMessageMaxRunes)
	if displayMessage == "" {
		displayMessage = "(no message)"
	}
	err := errors.Errorf(
		ctx,
		"agent reported status %q for phase %q: %s",
		status,
		phase,
		displayMessage,
	)
	return &statusError{
		err: err,
		fingerprint: []string{
			"agent-status-failed",
			string(phase),
			causePrefix(message),
		},
	}
}
```

### 3c. The bound and truncation helpers, if absent

```go
const (
	// statusMessageMaxRunes bounds the Result.Message copied into the
	// non-zero-exit error (and therefore into the pod log and the Sentry
	// event value). A failed ai_review result can carry a long rationale.
	statusMessageMaxRunes = 500

	// causePrefixMaxRunes bounds the cause component of the Sentry
	// fingerprint so unrelated failures stay in separate issues without
	// turning every distinct message into its own issue.
	causePrefixMaxRunes = 100
)

// truncateRunes returns s capped at max runes plus a trailing ellipsis. It
// counts runes, not bytes, so a multi-byte message is never cut mid-rune.
func truncateRunes(s string, max int) string {
	if max < 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// causePrefix returns a bounded, stable grouping key derived from
// Result.Message: the first line, trimmed, capped at causePrefixMaxRunes.
// An empty message yields "unknown" so the fingerprint is never empty.
func causePrefix(message string) string {
	line := message
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "unknown"
	}
	return truncateRunes(line, causePrefixMaxRunes)
}
```

### 3d. The fingerprinted error type and event processor, if absent

```go
type fingerprinter interface {
	Fingerprint() []string
}

type statusError struct {
	err         error
	fingerprint []string
}

func (e *statusError) Error() string { return e.err.Error() }

func (e *statusError) Unwrap() error { return e.err }

// Cause keeps github.com/bborbe/errors DataFromError traversal working.
func (e *statusError) Cause() error { return e.err }

func (e *statusError) Fingerprint() []string { return e.fingerprint }

func sentryFingerprintProcessor(
	event *sentry.Event,
	hint *sentry.EventHint,
) *sentry.Event {
	if event == nil || hint == nil || hint.OriginalException == nil {
		return event
	}
	var f fingerprinter
	if errors.As(hint.OriginalException, &f) {
		event.Fingerprint = f.Fingerprint()
	}
	return event
}
```

If `sentryFingerprintProcessor` is added, register it as the first statement in `main`, before `service.Main`:

```go
func main() {
	sentry.AddGlobalEventProcessor(sentryFingerprintProcessor)
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}
```

### 3e. The test assertion, if absent

Ensure `TestExitErrorForStatus` has a `message string` field and, for the failed case, asserts the message is present:

```go
if c.wantErr && !strings.Contains(err.Error(), c.message) {
	t.Fatalf("failed error must include Result.Message %q, got %q", c.message, err.Error())
}
```

Do not weaken `TestPrintRedactsAnthropicAuthToken`.

### 3f. The changelog entry, if absent

Add an `## Unreleased` entry above the newest released section describing that the failed result's message is now carried into the non-zero-exit error, that the non-zero exit for `failed` is unchanged, and that `needs_input` still exits 0.

## 4. Verify the acceptance criteria either way

Regardless of whether you changed code, confirm by reading the final files:

- A failed run (`agentlib.AgentStatusFailed`) produces a non-nil error whose `.Error()` contains the result message.
- `done`, `in_progress`, and `needs_input` produce a nil error (exit 0), unchanged.
- A message longer than the bound is truncated and still carries the prefix.
- The test suite covers the failed-message-present behavior.

</requirements>

<constraints>
- Keep the non-zero exit for `agentlib.AgentStatusFailed` — the executor records a phase success on exit 0 alone, so a failed run must still return a non-nil error.
- `needs_input`, `done`, and `in_progress` must still return nil from `exitErrorForStatus`. Do not change the parking behavior.
- Do NOT change any paging/alerting policy. The spec defers "which by-design escalations should page"; leave it deferred.
- Do NOT modify `cmd/run-task/main.go` — it has no status-to-exit mapping and is out of scope.
- Do NOT add a Prometheus metric, a config knob, an env var, or an opt-out flag.
- Do NOT duplicate any helper, constant, type, or processor that already exists in `package main`. Check with a grep before adding.
- Do NOT downgrade the `statusMessageMaxRunes` bound from 500 to 200, and do NOT change `exitErrorForStatus` to accept `*agentlib.Result`. The current shapes already satisfy the spec's intent.
- Wrap errors with `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- Existing tests must still pass. `make precommit` must pass.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run `make precommit` — must pass.

Run the focused tests directly:

```
go test -run 'TestExitErrorForStatus|TestExitErrorForStatusTruncatesLongMessage|TestStatusErrorFingerprint|TestSentryFingerprintProcessor' .
```

Confirm there is exactly one definition of each helper (no duplicates):

```
grep -n 'func truncateRunes\|func causePrefix\|func exitErrorForStatus\|type statusError\|func sentryFingerprintProcessor' main.go
```

Confirm the message is forwarded and included:

```
grep -n 'exitErrorForStatus(ctx, result.Status, phase, result.Message)' main.go
grep -n 'agent reported status %q for phase %q: %s' main.go
```

Confirm the failed-message assertion exists in the test:

```
grep -n 'must include Result.Message' main_internal_test.go
```

Then read the final `main.go` and confirm `exitErrorForStatus` returns nil for `done`, `in_progress`, and `needs_input`, and a non-nil error for `failed`.
</verification>
