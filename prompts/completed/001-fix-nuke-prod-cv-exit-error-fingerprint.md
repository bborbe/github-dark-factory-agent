---
status: completed
spec: [bug-nuke-prod-cv]
summary: exitErrorForStatus now carries a 500-rune-bounded Result.Message and a phase + cause-prefix Sentry fingerprint via a global event processor, while failed still exits non-zero and needs_input still exits 0
execution_id: a1faf338-0c3f-515c-80ba-dc1a244d89a2-exec-001-fix-nuke-prod-cv-exit-error-fingerprint
dark-factory-version: dev
created: "2026-11-11T18:33:36Z"
queued: "2026-11-11T18:45:41Z"
started: "2026-11-11T18:45:42Z"
completed: "2026-11-11T19:01:21Z"
---

<summary>
- A failed agent run's non-zero-exit error now carries the failed result's human-readable message, not just status and phase.
- The message is bounded so a long ai_review rationale cannot flood the pod log or the Sentry event value.
- Sentry events for failed runs are fingerprinted by phase plus a bounded cause prefix, so unrelated planning, execution, and ai_review failures stop collapsing into one umbrella issue.
- The process still exits non-zero when the agent publishes `failed`; the executor contract is unchanged.
- `needs_input` still exits 0 — parking for a human answer remains intentional and is not reclassified as a failure.
- No paging/alerting policy is changed; the spec defers the "which by-design escalations should page" decision.
- Existing exit-code tests keep passing and gain coverage for the new message and fingerprint behavior.
- A changelog entry records the behavior change.
</summary>

<objective>
Make `exitErrorForStatus` include a bounded form of `result.Message` in the error it returns, and give that error an explicit Sentry fingerprint of phase plus cause prefix so the identical capture-site stack no longer merges every failed run into one Sentry issue. Keep the non-zero exit for `failed` and leave `needs_input` exiting 0.
</objective>

<context>
Read `docs/dod.md` (definition of done) and `docs/design.md` (Part 4/Part 5 describe the phase contract and the exit-code contract) before editing.

Read these files in full before editing:

- `main.go` — the change is in `main`, `finishRun`, and `exitErrorForStatus`. The `application` struct, `Run`, and the existing comments around `exitErrorForStatus` are the contract you must preserve.
- `main_internal_test.go` — the existing plain-`testing` tests for this package. Extend `TestExitErrorForStatus` and add sibling tests there.
- `cmd/run-task/main.go` — read it only to confirm it is out of scope: it calls `agent.Run` and `agentlib.PrintResult` but never maps a status to an exit code, so it must not be changed.
- `CHANGELOG.md` — the top heading is `## v0.6.2`; there is no `## Unreleased` section today.

Read these guides before writing code or tests (these live in the YOLO container, not this repo):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — wrap with `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — table-driven plain tests in `package main` are the correct form here; see the note below on why Ginkgo cannot join.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format and prefix rules.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — changed-behavior coverage.

Why the tests stay plain `testing` in `main_internal_test.go`: `application`, `exitErrorForStatus`, `statusError`, and `fingerprinter` are unexported, so the assertions must live in `package main`. `main_test.go` already calls `RunSpecs`, and a second `RunSpecs` in the same test binary fails with "calling RunSpecs more than once". Do NOT add a Ginkgo suite to `main_internal_test.go`; follow the existing plain-test shape.

Verified source facts you must use exactly (read from the pinned modules, do not re-derive from memory):

- `agentlib.Result` (from `github.com/bborbe/agent@v0.138.0`, `agent_step.go`) has `Status AgentStatus`, `NextPhase string`, `Message string`, `ContinueToNext bool`, plus turn/interaction counters. `Message` is the human-readable failure cause.
- `finishRun(ctx context.Context, result *agentlib.Result, phase domain.TaskPhase) error` currently calls `exitErrorForStatus(ctx, result.Status, phase)`.
- `exitErrorForStatus(ctx context.Context, status agentlib.AgentStatus, phase domain.TaskPhase) error` currently returns `errors.Errorf(ctx, "agent reported status %q for phase %q", status, phase)` for `agentlib.AgentStatusFailed` and nil otherwise.
- `github.com/bborbe/errors` exposes `Errorf(ctx, format, args...) error`, `New(ctx, message) error`, `As(err, target) bool`. `errors.Errorf` returns a `DataError` wrapper that keeps a `Cause()` chain but does not expose a stack-trace method.
- Sentry stack trace behavior (verified in `github.com/getsentry/sentry-go@v0.50.0`, `exception.go`): if the outermost error has no extractable stack trace, `convertErrorToExceptions` calls `NewStacktrace()` at capture time. That is why every failure currently shares the same capture-site stack. The grouping fix must therefore be an explicit fingerprint, not a message-only change.
- `github.com/getsentry/sentry-go@v0.50.0` exposes:
  - `type EventProcessor func(event *Event, hint *EventHint) *Event`
  - `func AddGlobalEventProcessor(processor EventProcessor)` (deprecated but present; `.golangci.yml` already excludes `SA1019`)
  - `Event.Fingerprint []string`
  - `EventHint.OriginalException error`
- `service.Main` creates the Sentry client internally (`libsentry.NewClient(ctx, sentry.ClientOptions{...}, options.ExcludeErrors...)`) and `service.Service.Run` performs the single `CaptureException`. There is no per-client event-processor hook, which is why the global processor below is the least invasive wiring point.
- `libsentry.Client` (`github.com/bborbe/sentry@v1.10.7`) enriches events from `errors.DataFromError(hint.OriginalException)`; any wrapper you add must keep a `Cause() error` method so that traversal still works.

<!--
OPEN QUESTIONS FOR THE HUMAN REVIEWER (resolved best-effort from the spec; adjust at audit time if you disagree):

1. The spec says "include the truncated Result.Message" but gives no bound. This prompt uses 500 runes (`statusMessageMaxRunes`). It is a deliberate cap, not a tunable.
2. The spec says "cause prefix" but gives no definition. This prompt uses the first line of the message, trimmed, capped at 100 runes (`causePrefixMaxRunes`). If you want a different granularity (e.g. cut at the second ": " segment), change the helper and its table test.
3. The spec says "separately decide which by-design escalations should page". That decision is explicitly deferred here: no alerting/paging behavior changes, and `needs_input` keeps exiting 0.
4. The fingerprint starts with a fixed namespace token `agent-status-failed`. This is not required by the spec; it keeps the key readable and collision-free. Drop it if you prefer the raw phase+cause array.
-->

</context>

<requirements>

## 1. Add imports to `main.go`

Add to the standard-library import block:

```go
"strings"
```

Add to the third-party import block, alongside the existing `libsentry "github.com/bborbe/sentry"`:

```go
sentry "github.com/getsentry/sentry-go"
```

`goimports-reviser` (run by `make precommit`) will place it in the correct group; do not fight it.

## 2. Add the bounds and truncation helpers to `main.go`

Add below the existing `agentName` constant, before `main`:

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

Note: `pkg/steps_gh_token.go` already has a `truncate` helper, but it is in package `pkg` and is not importable from `package main`. Do not try to reuse it.

## 3. Add the fingerprinted error type to `main.go`

Add next to `exitErrorForStatus`:

```go
// fingerprinter is implemented by errors that know their Sentry grouping key.
type fingerprinter interface {
	Fingerprint() []string
}

// statusError is the non-zero-exit error for a failed agent result. The
// capture-site stack is identical for every failure, so Sentry would merge
// unrelated planning / execution / ai_review causes into one issue; this type
// carries an explicit fingerprint (phase + bounded cause prefix) that
// sentryFingerprintProcessor installs on the event.
type statusError struct {
	err         error
	fingerprint []string
}

func (e *statusError) Error() string { return e.err.Error() }

// Unwrap keeps errors.Is / errors.As working through the wrapper.
func (e *statusError) Unwrap() error { return e.err }

// Cause keeps github.com/bborbe/errors DataFromError traversal working.
func (e *statusError) Cause() error { return e.err }

// Fingerprint is the Sentry grouping key for this failure.
func (e *statusError) Fingerprint() []string { return e.fingerprint }
```

## 4. Update `exitErrorForStatus`

Replace the existing function with the four-argument form. Keep the existing GoDoc intent (the executor records a phase success on exit 0 alone; `needs_input` is deliberately not a failure) and add the new message/fingerprint behavior:

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

The `failed` path still returns a non-nil error; `done`, `in_progress`, and `needs_input` still return nil. Do not change those branches.

## 5. Update `finishRun`

Pass the result message through:

```go
return exitErrorForStatus(ctx, result.Status, phase, result.Message)
```

No other change to `finishRun`.

## 6. Add the Sentry event processor and register it in `main`

Add the processor next to `statusError`:

```go
// sentryFingerprintProcessor installs the fingerprint carried by a
// fingerprinter error onto the Sentry event. It is a no-op for every other
// error, so it can be registered globally: service.Main owns the Sentry client
// and exposes no per-client event-processor hook.
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

Register it as the first statement in `main`, before the application is built:

```go
func main() {
	sentry.AddGlobalEventProcessor(sentryFingerprintProcessor)
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}
```

The processor runs after `libsentry`'s tag-enrichment processor (client processors run before global processors in `prepareEvent`), so it sees the original error unchanged. `service.Service.Run` sets `OriginalException` on the hint, so the processor sees the `*statusError` returned by `Run`.

## 7. Update and extend `main_internal_test.go`

Add `"errors"`, `"slices"`, `"strings"` to the standard-library imports and `sentry "github.com/getsentry/sentry-go"` to the third-party imports.

### 7a. Update the existing `TestExitErrorForStatus`

Add a `message string` field to the table and pass it as the fourth argument. Keep all four existing cases and their assertions. Add a message assertion for the failing case:

```go
cases := []struct {
	name    string
	status  agentlib.AgentStatus
	message string
	wantErr bool
}{
	{
		name:    "failed must fail the Job so the executor does not record a phase success",
		status:  agentlib.AgentStatusFailed,
		message: "execution: ensure worktree: clone boom",
		wantErr: true,
	},
	{
		name:    "done exits 0",
		status:  agentlib.AgentStatusDone,
		wantErr: false,
	},
	{
		name:    "in_progress exits 0",
		status:  agentlib.AgentStatusInProgress,
		wantErr: false,
	},
	{
		// needs_input reaches the same park path on purpose: the task is meant
		// to wait at human_review for an answer, not to be re-driven.
		name:    "needs_input exits 0 — parking for a human answer is its intent",
		status:  agentlib.AgentStatusNeedsInput,
		wantErr: false,
	},
}

for _, c := range cases {
	t.Run(c.name, func(t *testing.T) {
		err := exitErrorForStatus(ctx, c.status, phase, c.message)
		if c.wantErr && err == nil {
			t.Fatalf(
				"status %q: expected a non-nil error, got nil — the Job would exit 0 and the executor would record it as a phase success",
				c.status,
			)
		}
		if !c.wantErr && err != nil {
			t.Fatalf("status %q: expected nil, got %v", c.status, err)
		}
		if c.wantErr && !strings.Contains(err.Error(), c.message) {
			t.Fatalf("failed error must include Result.Message %q, got %q", c.message, err.Error())
		}
	})
}
```

### 7b. Add `TestExitErrorForStatusTruncatesLongMessage`

```go
func TestExitErrorForStatusTruncatesLongMessage(t *testing.T) {
	ctx := context.Background()
	message := strings.Repeat("a", statusMessageMaxRunes+100)

	err := exitErrorForStatus(
		ctx,
		agentlib.AgentStatusFailed,
		domain.TaskPhase("ai_review"),
		message,
	)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), strings.Repeat("a", statusMessageMaxRunes)+"...") {
		t.Fatalf(
			"want the message truncated at %d runes with an ellipsis, got %q",
			statusMessageMaxRunes,
			err.Error(),
		)
	}
	if strings.Contains(err.Error(), strings.Repeat("a", statusMessageMaxRunes+1)) {
		t.Fatalf("message was not truncated: %q", err.Error())
	}
}
```

### 7c. Add `TestStatusErrorFingerprint`

```go
func TestStatusErrorFingerprint(t *testing.T) {
	ctx := context.Background()

	err := exitErrorForStatus(
		ctx,
		agentlib.AgentStatusFailed,
		domain.TaskPhase("execution"),
		"execution: ensure worktree: clone boom",
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	var f fingerprinter
	if !errors.As(err, &f) {
		t.Fatalf("expected a fingerprinter error, got %T", err)
	}
	want := []string{
		"agent-status-failed",
		"execution",
		"execution: ensure worktree: clone boom",
	}
	if !slices.Equal(f.Fingerprint(), want) {
		t.Fatalf("fingerprint = %v, want %v", f.Fingerprint(), want)
	}
}
```

### 7d. Add `TestSentryFingerprintProcessor`

```go
func TestSentryFingerprintProcessor(t *testing.T) {
	ctx := context.Background()

	t.Run("sets the fingerprint from a status error", func(t *testing.T) {
		err := exitErrorForStatus(
			ctx,
			agentlib.AgentStatusFailed,
			domain.TaskPhase("planning"),
			"planning: PR #7 is not a draft",
		)
		event := &sentry.Event{}
		got := sentryFingerprintProcessor(
			event,
			&sentry.EventHint{OriginalException: err},
		)
		want := []string{
			"agent-status-failed",
			"planning",
			"planning: PR #7 is not a draft",
		}
		if !slices.Equal(got.Fingerprint, want) {
			t.Fatalf("fingerprint = %v, want %v", got.Fingerprint, want)
		}
	})

	t.Run("leaves an unrelated error without a fingerprint", func(t *testing.T) {
		event := &sentry.Event{}
		got := sentryFingerprintProcessor(
			event,
			&sentry.EventHint{OriginalException: errors.New("boom")},
		)
		if len(got.Fingerprint) != 0 {
			t.Fatalf("unexpected fingerprint %v", got.Fingerprint)
		}
	})

	t.Run("nil event and hint are safe", func(t *testing.T) {
		if got := sentryFingerprintProcessor(nil, nil); got != nil {
			t.Fatalf("expected nil event, got %v", got)
		}
	})
}
```

Do not remove or weaken `TestPrintRedactsAnthropicAuthToken`.

## 8. Add a CHANGELOG entry

Add a new `## Unreleased` section between the intro paragraph and `## v0.6.2`:

```markdown
## Unreleased

- fix: **carry the failed result's message into the non-zero-exit error and fingerprint the Sentry event by phase + cause prefix.** `exitErrorForStatus` built its error from status and phase only, so every failed run produced the same capture-site stack and Sentry merged unrelated planning / execution / ai_review causes into one umbrella issue (`NUKE-PROD-CV`). The error now includes a 500-rune-bounded `Result.Message`, and a global Sentry event processor sets `event.Fingerprint` to `["agent-status-failed", <phase>, <first-line cause prefix, 100 runes>]`. The non-zero exit for `failed` is unchanged, and `needs_input` still exits 0 because parking for a human answer is its intent. Paging policy is deliberately unchanged
```

</requirements>

<constraints>
- Keep the non-zero exit for `agentlib.AgentStatusFailed` — the executor records a phase success on exit 0 alone, so a failed run must still return a non-nil error.
- `needs_input`, `done`, and `in_progress` must still return nil from `exitErrorForStatus`. Do not change the parking behavior.
- Do NOT change any paging/alerting policy. The spec defers "which by-design escalations should page"; leave it deferred.
- Do NOT modify `cmd/run-task/main.go` — it has no status-to-exit mapping and is out of scope.
- Do NOT add a Prometheus metric, a config knob, an env var, or an opt-out flag. The spec asks for a bounded message and a fingerprint only.
- Wrap errors with `github.com/bborbe/errors`; no bare `return err`, no `fmt.Errorf`.
- Existing tests must still pass. `make precommit` must pass.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run `make precommit` — must pass.

Also run the focused tests directly (they must pass even if the full precommit toolchain is unavailable):

```
go test -run 'TestExitErrorForStatus|TestExitErrorForStatusTruncatesLongMessage|TestStatusErrorFingerprint|TestSentryFingerprintProcessor' .
```

Confirm by reading the final `main.go` that:
- `exitErrorForStatus` returns a non-nil error for `agentlib.AgentStatusFailed` and nil for the other three statuses.
- The returned error's message contains the (truncated) result message.
- `main` registers `sentryFingerprintProcessor` before calling `service.Main`.
</verification>
