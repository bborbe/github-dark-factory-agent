---
status: draft
---

<summary>
- The retry transport's total sleep budget now fits inside the HTTP client's timeout, not just a single wait
- A repeated short Retry-After no longer accumulates past the client deadline and surfaces as a context-deadline error
- Long waits still defer, so the caller sees the real 403 or 429 promptly
- Short waits are still slept and retried
- Behaviour is otherwise unchanged
</summary>

<objective>
Lower the retry transport's per-wait bound so the worst-case total sleep across all retries stays inside each client's timeout.
</objective>

<context>
Read CLAUDE.md for project conventions.

`pkg/github.go` contains `retryTransportFor(apiBase)`, which builds `libhttp.RetryBackoffOptions{RetryHosts: hosts, MaxWait: 5 * time.Second}`. Both GitHub clients use it: `newGitHubClient` with `Timeout: 15 * time.Second` and `newGHTokenCheckStep` with `Timeout: 10 * time.Second`.

⚠️ **`MaxWait` bounds a single wait, not the call.** `MaxAttempts` defaults to 5, so the worst case is 5 × `MaxWait` of sleeping inside one `http.Client.Do` — with `MaxWait: 5s` that is **25 seconds**, against a 15-second and a 10-second timeout. A server sending a repeated short `Retry-After` therefore still exhausts the deadline and surfaces `retry wait cancelled: context deadline exceeded` with a nil response, which is the failure the `MaxWait` field was added to prevent. It is only safe per wait, not per request.
</context>

<requirements>
1. In `retryTransportFor` in `pkg/github.go`, change `MaxWait: 5 * time.Second` to **`MaxWait: 1 * time.Second`**.

   One second, not two: five retries at one second is a five-second worst-case sleep, which leaves ten seconds of the 15-second client and five seconds of the 10-second client for the requests themselves. Two seconds would consume ten of the token client's ten seconds on sleeping alone.

2. Extend the helper's doc comment so it states the budget arithmetic rather than only the per-wait rule — that `MaxAttempts` (default 5) multiplies the wait, so `MaxWait` is chosen so `MaxAttempts × MaxWait` stays inside the smaller of the two client timeouts.

3. Update the existing tests in `pkg/github_test.go` so they still describe the new bound:
   - The deferral case currently uses `Retry-After: 60`; keep it, since 60s still exceeds any `MaxWait`.
   - Add one case proving the **accumulation** bound: a stub that returns `429` with `Retry-After: 1` on every request, for more attempts than the client timeout allows. Assert the call returns within the client's timeout rather than blocking, and that the error is the rate-limit error (`HTTP 429`) rather than a `context deadline exceeded` cancellation. This is the case that fails at `MaxWait: 5s` and passes at `1s`.
   - Leave the short-retry (two requests) and plain-403 cases unchanged.

4. Do not change `RetryHosts`, the client `Timeout` values, the dependency version, error wording, or any exported signature.

5. Update the `## Unreleased` bullet in `CHANGELOG.md` so it states the bound in terms of the total retry budget, not a single wait.

6. Before finishing, re-run `<verification>` and confirm it passes; then walk each numbered requirement against the change and report any that is unmet.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must pass unchanged apart from the new case
- Do not touch any file outside `pkg/github.go`, `pkg/github_test.go` and `CHANGELOG.md`
- Do not raise or remove the client `Timeout` values — the bound belongs on the retry wait
- Add no dependency
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
</verification>
