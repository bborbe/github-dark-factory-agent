---
status: completed
summary: Wired both GitHub HTTP clients through bborbe/http's rate-limit retry transport with a five-second MaxWait deferral bound, bumped the dep to v1.28.1, added retry/deferral tests and a CHANGELOG entry
execution_id: github-dark-factory-agent-retry-exec-001-wire-retry-roundtripper
dark-factory-version: v0.198.17
created: "2026-11-27T12:44:08Z"
queued: "2026-11-27T12:44:08Z"
started: "2026-11-27T12:45:38Z"
completed: "2026-11-27T12:51:37Z"
---

<summary>
- Both GitHub HTTP clients this agent builds now retry a rate-limit response instead of returning the error
- A 429 or a rate-limiting 403 from api.github.com is retried with the wait the response asks for, up to five times
- A wait longer than five seconds is deferred, so the caller sees the real rate-limit response promptly instead of hanging until the client's timeout
- Each retry emits one log line naming the attempt, the status, the class and the wait
- The rate-limit class label is the corrected one, so a 429 carrying positive remaining quota reports secondary rather than unclassified
</summary>

<objective>
Route both of this agent's GitHub HTTP clients through the shared rate-limit-aware retry transport, so a throttled response delays the agent instead of ending its work item.
</objective>

<context>
Read the project conventions before starting.

Two GitHub clients are built in `pkg/`, each as a bare `&http.Client{...}`:
- `pkg/github.go` — `newGitHubClient(token, baseURL)` (~line 47), client literal at ~52, `Timeout: 15 * time.Second`, `baseURL` defaulting to `apiBaseURL`.
- `pkg/steps_gh_token.go` — `newGHTokenCheckStep(ghToken, httpURL)` (~line 56), client literal at ~60, `Timeout: 10 * time.Second`, probing `rateLimitURL`.

Both are exposed for tests by `pkg/export_test.go` as `NewGitHubClientWithBaseURL(token, baseURL) GitHubClient` and `NewGHTokenCheckStepWithURL(token, httpURL) agentlib.Step`.

`github.com/bborbe/http` is pinned at v1.26.32 as an indirect dependency. Its `NewRoundTripperRetryWithBackoff(roundTripper, RetryBackoffOptions)` returns a `http.RoundTripper` that retries a 403/429 from the hosts in `RetryHosts`, sleeping the `Retry-After` duration (and the `X-RateLimit-Reset` delta for primary exhaustion), up to `MaxAttempts` (default 5), and emits `retry attempt=<n> status=<status> class=<class> wait=<duration>` per attempt.

⚠️ **`MaxWait` is load-bearing here, not optional.** `http.Client.Timeout` covers the whole call including the sleep the retry transport performs inside `RoundTrip`. With `MaxWait` left at its five-minute default, a 60-second `Retry-After` — GitHub's typical secondary-limit value — sleeps until the client deadline fires and returns `context deadline exceeded` with a nil response, which is worse than the fast error it replaces. `MaxWait` must sit well below each client's timeout.

This repository has no `vendor/` directory, so the dependency bump needs `go mod tidy` and nothing else.
</context>

<requirements>
1. In `pkg/github.go`, add one unexported helper:

   ```go
   // retryTransportFor wraps the default transport with the shared rate-limit
   // retry round tripper. api.github.com is always retried; the host of apiBase
   // is added too when it differs, so a test seam pointing at a stub server is
   // covered. MaxWait stays well below the client timeouts: a longer server
   // requested wait is deferred, so the caller sees the real 403/429 instead of
   // a context-deadline error when the client timeout fires mid-sleep.
   func retryTransportFor(apiBase string) http.RoundTripper {
       hosts := []string{"api.github.com"}
       if u, err := url.Parse(apiBase); err == nil && u.Hostname() != "" && u.Hostname() != "api.github.com" {
           hosts = append(hosts, u.Hostname())
       }
       return libhttp.NewRoundTripperRetryWithBackoff(
           http.DefaultTransport,
           libhttp.RetryBackoffOptions{RetryHosts: hosts, MaxWait: 5 * time.Second},
       )
   }
   ```

   Import it as `libhttp "github.com/bborbe/http"`, and add `net/url` / `time` if missing.

2. Wire **both** client literals:
   - `pkg/github.go` `newGitHubClient`: `client: &http.Client{Timeout: 15 * time.Second, Transport: retryTransportFor(baseURL)}`.
   - `pkg/steps_gh_token.go` `newGHTokenCheckStep`: `client: &http.Client{Timeout: 10 * time.Second, Transport: retryTransportFor(httpURL)}`. This is a GitHub API call like the other — do not leave it unwrapped, and do not invent an exemption for it.

3. Do not change request building, header sets, error wording, sentinel errors, or any exported signature. `newGitHubClient` and `newGHTokenCheckStep` keep their current signatures.

4. Raise the dependency to the released tag: `go get github.com/bborbe/http@v1.28.1` then `go mod tidy`. **v1.28.1 is required, not incidental** — v1.28.0 mislabels a 429 carrying positive `X-RateLimit-Remaining` with no `Retry-After` as `unclassified`, and that is the label the retry line carries. It may not be in the local module cache, so allow the fetch. Confirm `go.mod` lists `github.com/bborbe/http v1.28.1` in the direct `require` block.

5. Add tests in `pkg/`, using the existing exported aliases from `export_test.go`:
   a. **Retry fires.** `NewGitHubClientWithBaseURL` pointed at an `httptest.Server` returning `429` with `Retry-After: 1` on the first request and a valid `200` on the second; assert **exactly two requests** reached the server and the call succeeded. Same shape for `NewGHTokenCheckStepWithURL` — but note that step returns an `*agentlib.Result`, not a Go error, so assert its status is the success value rather than asserting `err == nil`.
   b. **Long wait defers — GitHub client only.** The token step returns a `Result` rather than an error, so this case and the next apply to `NewGitHubClientWithBaseURL` alone. Stub returns `429` with `Retry-After: 60` on every request; assert the call returns an error whose text contains **`HTTP 429`** (this repo's wording is `github returned HTTP %d`, not `status %d`), **exactly one** request reached the server, and elapsed time is under five seconds — the timing assertion is what discriminates, because the request count is identical with or without `MaxWait` (the client deadline kills the call either way), and the text differs too: without `MaxWait` the error is a `context deadline exceeded` cancellation.
   c. **Plain 403 is not retried — GitHub client only.** Stub returns `403` with no `Retry-After` and no `X-RateLimit-Remaining`; assert the error contains **`HTTP 403`** and exactly one request.

   Match the surrounding Ginkgo v2 / Gomega style.

6. Update `CHANGELOG.md` with one `## Unreleased` bullet describing the retry wiring and the five-second deferral bound. If no `## Unreleased` section exists, create one directly above the newest released `## vX.Y.Z` heading; do not rename or renumber any released heading.

7. Before finishing, re-run `<verification>` and confirm it passes; then walk each numbered requirement against the change and report any that is unmet.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must pass unchanged
- Do not touch any file outside `pkg/github.go`, `pkg/steps_gh_token.go`, their test files, `go.mod`, `go.sum` and `CHANGELOG.md`
- Do not raise or remove the client `Timeout` values — the bound belongs on the retry wait, not on the client
- Add no dependency beyond the `bborbe/http` bump
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
</verification>
