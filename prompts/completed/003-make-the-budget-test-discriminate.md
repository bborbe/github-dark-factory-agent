---
status: completed
summary: 'Changed the accumulation-bound test to Retry-After: 4 with deferred-behaviour assertions (one request, <5s, HTTP 429 without deadline error), verified it fails under a 5s MaxWait overlay and passes at 1s'
execution_id: github-dark-factory-agent-retry-exec-003-make-the-budget-test-discriminate
dark-factory-version: v0.198.17
created: "2026-11-27T14:28:23Z"
queued: "2026-11-27T14:28:23Z"
started: "2026-11-27T14:29:10Z"
completed: "2026-11-27T14:33:33Z"
---

<summary>
- The accumulation-bound test now actually fails when the retry budget is too large, instead of passing either way
- It uses a wait that is slept at the old bound but defers at the new one
- The production bound is unchanged
</summary>

<objective>
Make the accumulation-bound test discriminate between a five-second and a one-second per-wait bound.
</objective>

<context>
Read CLAUDE.md for project conventions.

`pkg/github.go`'s `retryTransportFor` sets `MaxWait: 1 * time.Second`, with `MaxAttempts` defaulting to 5, so the worst-case total sleep is five seconds inside a 15-second client timeout.

`pkg/github_test.go` has an `It("bounds the accumulated retry sleep inside the client timeout", …)` case. It stubs `Retry-After: 1` and asserts the call returns `HTTP 429` within 15 seconds with six requests.

⚠️ **That case does not discriminate.** A one-second `Retry-After` is slept once per attempt — 5 × 1s = 5s total — which fits inside the 15-second timeout at *either* bound. Verified empirically: re-running the suite with `MaxWait` reverted to `5 * time.Second` (via `go test -overlay`, worktree untouched) still passes. So the test cannot catch a regression to the larger bound.
</context>

<requirements>
1. In `pkg/github_test.go`, change the accumulation-bound case's stub header from `Retry-After: 1` to **`Retry-After: 4`**, and update its comment to explain the arithmetic: four seconds is at or below the old five-second bound, so it was slept five times (20 seconds, past the 15-second client timeout), while at the one-second bound it exceeds `MaxWait` and is deferred immediately.

2. Update the case's assertions to match the deferred behaviour at the current bound:
   - the call returns an error whose text contains `HTTP 429`;
   - the text does **not** contain `context deadline exceeded`;
   - **exactly one** request reached the server — a deferred wait returns the response rather than retrying;
   - elapsed time is under five seconds.

   The `requests` count changes from six to one; that is the point of the case, not an incidental edit.

3. Do not change `MaxWait`, `RetryHosts`, `MaxAttempts`, the client timeouts, or any other test case.

4. Before finishing, re-run `<verification>` and confirm it passes; then walk each numbered requirement against the change and report any that is unmet.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Only `pkg/github_test.go` changes
- Every other test must pass unchanged
- Add no dependency
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
</verification>
