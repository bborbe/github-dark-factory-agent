---
status: completed
spec: [bug-nuke-dev-g9]
summary: exitErrorForStatus now includes Result.Message and phase/status error data, a global per-phase Sentry fingerprint processor is registered in main, and unit plus end-to-end capture tests and a CHANGELOG entry cover the change.
execution_id: 34cfba53-59ac-5b92-a6ed-3cb63eab9735-exec-001-bug-nuke-dev-g9-sentry-phase-fingerprint
dark-factory-version: dev
created: "2026-11-11T19:30:00Z"
queued: "2026-11-11T19:40:48Z"
started: "2026-11-11T19:40:49Z"
completed: "2026-11-11T20:03:30Z"
---

<summary>
- A failed agent phase now reports the phase's own message in the error, so the Sentry issue says why the phase failed instead of only that the status was "failed".
- Sentry groups failures separately for planning, execution, and ai_review, so one phase's failures no longer disappear into another phase's issue.
- Each failure event carries the phase and status as Sentry tags, making the event self-describing when it arrives.
- Escalation is unchanged: a failed result still makes the Job exit non-zero.
- Events from unrelated errors are not re-grouped; only errors that carry a phase are fingerprinted.
- The fix works through the existing `service.Main` capture path without double-reporting the same failure.
- Tests cover the message in the error, the phase/status data on the error, the fingerprint processor, and a real capture through the Sentry client.
- A changelog entry records the operator-visible change.
- ai_review fail-closed routing is deliberately untouched in this change.
</summary>

<objective>
Make a failed phase's Sentry event self-describing and separable: include `Result.Message` in the error raised by `exitErrorForStatus`, attach the phase and status to the error so they become Sentry tags, and assign a per-phase Sentry fingerprint so planning/execution/ai_review failures no longer collapse into one untriageable issue. The Job must still exit non-zero on failure.
</objective>

<context>
Read these files before changing anything.

Project:
- `main.go` — the Kafka entry point. Read `main`, `application.Run`, `finishRun`, and `exitErrorForStatus`. This is the only place the bug lives.
- `main_internal_test.go` — the existing `TestExitErrorForStatus` guards the exit-code contract; it is plain `testing` in `package main` on purpose (the external `main_test` package already calls `RunSpecs`, so a second suite would fail).
- `pkg/steps_gh_token.go` — `failed(msg)` and `needsInput(msg)` build `*agentlib.Result` with `Status` and `Message`. These are the messages that must now reach Sentry.
- `pkg/steps_ai_review.go` — `claudeVerdict`, `writeAndRoute`, `review`, `routeRecorded`. Read the messages they produce (e.g. `ai_review: review surfaced blocking concerns: …`, `ai_review: PR #N is no longer a draft …`) so you can assert the real messages reach the error. Do NOT change this file.
- `cmd/run-task/main.go` — the local-CLI sibling entry point. It does NOT call `exitErrorForStatus` and is not a K8s Job; it is out of scope for this change.
- `docs/dod.md` — project Definition of Done (precommit, errors via bborbe/errors, escalation doctrine, changelog).
- `CHANGELOG.md` — release-notes format; add an `## Unreleased` section at the top.

Libraries (verify, do not trust this paraphrase):
- `github.com/bborbe/service` `service.Main` / `service.Run`: after `app.Run` returns an error, it calls `sentryClient.CaptureException(err, &sentry.EventHint{Context: ctx, OriginalException: err}, sentry.NewScope())`. The scope is created by the framework, so the application cannot attach a fingerprint to that capture through the scope.
- `github.com/bborbe/sentry` `NewClient` registers an `enrichEventTags` client event processor that copies `github.com/bborbe/errors` error data into `event.Tags`.
- `github.com/getsentry/sentry-go` (indirect today): `AddGlobalEventProcessor(func(*Event, *EventHint) *Event)`, `Event.Fingerprint []string`, `Event.Tags map[string]string`, `EventHint.OriginalException error`. Global processors run after the client processors and after the scope is applied, which is the only hook the application controls without double-capturing.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
</context>

<requirements>
1. In `main.go`, change `exitErrorForStatus` to accept the phase's message and to attach phase/status data to the error. Keep the existing "failed only" contract: `done`, `in_progress`, and `needs_input` still return `nil`.

   Replace the existing function body with this shape (keep the existing doc comment, extend it to explain the message and data):

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
       msg := strings.TrimSpace(message)
       var err error
       if msg == "" {
           err = errors.Errorf(ctx, "agent reported status %q for phase %q", status, phase)
       } else {
           err = errors.Errorf(
               ctx,
               "agent reported status %q for phase %q: %s",
               status,
               phase,
               msg,
           )
       }
       return errors.AddDataToError(err, map[string]any{
           "phase":  string(phase),
           "status": string(status),
       })
   }
   ```

   `errors` is `github.com/bborbe/errors`; `AddDataToError` returns a `DataError` whose `Data()` is read by `libsentry`'s `enrichEventTags` and by the fingerprint processor below. Add `"strings"` to the imports.

2. Update the caller in `finishRun` to pass the message:

   ```go
   return exitErrorForStatus(ctx, result.Status, phase, result.Message)
   ```

   Do not change `finishRun`'s signature or the fact that its error is returned from `application.Run` unchanged (do not wrap it before returning, or the Sentry event hint will see a wrapper instead of the `DataError`).

3. Add a per-phase Sentry fingerprint processor to `main.go` and register it in `main()` before `service.Main`.

   ```go
   // phaseFingerprintProcessor is a global Sentry event processor that assigns
   // a per-phase fingerprint to the event captured from a failed phase.
   //
   // service.Main captures the error Run returns with a fresh
   // sentry.NewScope(), so the application cannot attach a fingerprint to that
   // capture through the scope. A global event processor is the only hook that
   // runs after that scope is applied and before the event is sent. It leaves
   // every event without phase data untouched, so it cannot re-group unrelated
   // captures.
   func phaseFingerprintProcessor(
       event *sentrygo.Event,
       hint *sentrygo.EventHint,
   ) *sentrygo.Event {
       if event == nil || hint == nil || hint.OriginalException == nil {
           return event
       }
       phase, ok := errors.DataFromError(hint.OriginalException)["phase"].(string)
       if !ok || phase == "" {
           return event
       }
       event.Fingerprint = []string{"github-dark-factory-agent", "phase", phase}
       return event
   }
   ```

   In `main()`:

   ```go
   func main() {
       sentrygo.AddGlobalEventProcessor(phaseFingerprintProcessor)
       app := &application{}
       os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
   }
   ```

   Add the import `sentrygo "github.com/getsentry/sentry-go"` (alias it so it does not collide with the existing `libsentry "github.com/bborbe/sentry"`). `go mod tidy` (run by `make precommit`) will promote `github.com/getsentry/sentry-go` from indirect to direct in `go.mod`; do not hand-edit `go.mod`.

4. Update `main_internal_test.go`'s `TestExitErrorForStatus`:
   - Add a `message string` field to the table cases.
   - Give the failed case a real message such as `"git clone --bare: connection refused"`.
   - Call `exitErrorForStatus(ctx, c.status, phase, c.message)`.
   - For the failed case, assert:
     - `err.Error()` contains `c.message` and `string(phase)` (this is the actual bug: the message used to be dropped).
     - `errors.DataFromError(err)["phase"] == string(phase)`.
     - `errors.DataFromError(err)["status"] == string(c.status)`.
   - For every non-failed case, assert `err == nil` as before.
   - Add imports `"github.com/bborbe/errors"` and `sentrygo "github.com/getsentry/sentry-go"`.

5. Add a direct unit test for the processor's negative case in `main_internal_test.go`:

   ```go
   func TestPhaseFingerprintProcessorIgnoresUnrelatedErrors(t *testing.T) {
       event := phaseFingerprintProcessor(
           &sentrygo.Event{},
           &sentrygo.EventHint{OriginalException: errors.New(context.Background(), "unrelated")},
       )
       if len(event.Fingerprint) != 0 {
           t.Fatalf("unrelated error must not be fingerprinted, got %v", event.Fingerprint)
       }
   }
   ```

6. Add an end-to-end test in `main_internal_test.go` that proves the fingerprint and tags reach a real Sentry event. Register the processor, build a real `libsentry.NewClient` with a recording transport, capture the error produced by `exitErrorForStatus`, and assert the recorded event. Use these exact types (verify the `sentrygo.Transport` interface in the module cache if it has changed):

   ```go
   type recordingTransport struct {
       events []*sentrygo.Event
   }

   func (t *recordingTransport) Configure(_ sentrygo.ClientOptions) {}
   func (t *recordingTransport) SendEvent(event *sentrygo.Event) {
       t.events = append(t.events, event)
   }
   func (t *recordingTransport) Flush(_ time.Duration) bool { return true }
   func (t *recordingTransport) FlushWithContext(_ context.Context) bool { return true }
   func (t *recordingTransport) Close() {}
   ```

   Test body:

   ```go
   func TestPhaseFingerprintReachesSentryEvent(t *testing.T) {
       ctx := context.Background()
       sentrygo.AddGlobalEventProcessor(phaseFingerprintProcessor)

       transport := &recordingTransport{}
       client, err := libsentry.NewClient(ctx, sentrygo.ClientOptions{
           Dsn:       "https://key@example.com/1",
           Transport: transport,
       })
       if err != nil {
           t.Fatalf("create sentry client: %v", err)
       }
       defer func() { _ = client.Close() }()

       phase := domain.TaskPhase("ai_review")
       failure := exitErrorForStatus(
           ctx,
           agentlib.AgentStatusFailed,
           phase,
           "ai_review: review surfaced blocking concerns: timeout",
       )
       client.CaptureException(
           failure,
           &sentrygo.EventHint{Context: ctx, OriginalException: failure},
           sentrygo.NewScope(),
       )

       if len(transport.events) != 1 {
           t.Fatalf("want 1 captured event, got %d", len(transport.events))
       }
       event := transport.events[0]
       if len(event.Fingerprint) != 3 ||
           event.Fingerprint[0] != "github-dark-factory-agent" ||
           event.Fingerprint[1] != "phase" ||
           event.Fingerprint[2] != "ai_review" {
           t.Fatalf("unexpected fingerprint: %v", event.Fingerprint)
       }
       if event.Tags["phase"] != "ai_review" {
           t.Fatalf("want phase tag ai_review, got %q", event.Tags["phase"])
       }
       if event.Tags["status"] != "failed" {
           t.Fatalf("want status tag failed, got %q", event.Tags["status"])
       }
   }
   ```

   Imports this test needs: `time`, `libsentry "github.com/bborbe/sentry"`, `sentrygo "github.com/getsentry/sentry-go"`.

7. Add a changelog entry. Insert an `## Unreleased` section immediately after the CHANGELOG intro and before `## v0.6.2`:

   ```markdown
   ## Unreleased

   - fix: **name the failing phase and its message in the failure Sentry event, and fingerprint failures per phase.** `exitErrorForStatus` raised `agent reported status "failed" for phase "execution"` with no `Result.Message`, so planning/execution/ai_review failures collapsed into one untriageable issue. The error now carries the phase's message plus `phase`/`status` tags, and a Sentry event processor assigns `github-dark-factory-agent/phase/<phase>` as the fingerprint. Escalation is unchanged — a failed result still exits non-zero.
   ```

8. Run the targeted tests, then `make precommit` (see `<verification>`).
</requirements>

<constraints>
- Copy of the spec's constraint: do NOT suppress the escalation. A failed result must still return a non-nil error so the Job exits non-zero. Do not make `exitErrorForStatus` return `nil` for `AgentStatusFailed`.
- Do NOT change `pkg/steps_ai_review.go` in this prompt. The spec labels retry/classify of transient ai_review failures a follow-up; the `claude` runner already retries rate limits internally (`maxRateLimitReinvokes`), so a naive retry would duplicate existing policy. ai_review fail-closed routing stays exactly as it is.
- `cmd/run-task/main.go` is out of scope: it does not call `exitErrorForStatus` and is a local CLI, not a K8s Job.
- Do NOT add config fields, env vars, flags, or tunable knobs. The fingerprint is a fixed function of the phase.
- Do NOT add a `## Failure` section and do NOT mutate `assignee` or `status` (escalation doctrine in `docs/dod.md`).
- Use `github.com/bborbe/errors` for errors; no bare `return err` and no `fmt.Errorf`.
- Do not wrap the error returned by `finishRun` before it leaves `application.Run`; the Sentry event hint must see the `DataError` carrying `phase`/`status`.
- Existing tests must still pass.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run the targeted tests first:

```bash
go test -mod=mod . -run 'TestExitErrorForStatus|TestPhaseFingerprintProcessor|TestPhaseFingerprintReachesSentryEvent' -v
```

Then run the full pre-commit gate:

```bash
make precommit
```

Both must pass. `make precommit` runs `go mod tidy`, so the direct `github.com/getsentry/sentry-go` import is resolved there; do not hand-edit `go.mod`.
</verification>

<!--
OPEN QUESTIONS surfaced by the prompt generator; resolve at audit time.

1. The spec labels "retry/classify transient ai_review failures before failing
   closed" a FOLLOW-UP. This prompt deliberately does not change ai_review
   fail-closed routing. The `claude` runner already retries rate limits
   internally (`maxRateLimitReinvokes` in the agent module), so a naive retry
   would duplicate that policy. If the reviewer wants the follow-up in this
   batch, it needs its own prompt with a defined transient classifier (which
   error shapes count as transient) and a retry budget that still fits the
   phase deadline.

2. The human-takeover branch (PR no longer a draft, `pkg/steps_ai_review.go`)
   is arguably a successful handoff rather than a failure. The spec says
   "do not suppress the escalation" for this change, so it is left unchanged.
   Confirm whether a later change should route it to `human_review` instead of
   `failed`.

3. The fingerprint is per phase only (planning / execution / ai_review). If the
   reviewer wants to separate failure CAUSES within a phase (e.g. clone failure
   vs review concerns), the fingerprint needs a stable cause key, which
   `Result.Message` does not provide as a structured value.
-->
