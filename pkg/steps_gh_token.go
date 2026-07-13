// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
)

// rateLimitURL is the GitHub endpoint used to verify the token. The
// rate_limit endpoint itself does NOT count against the rate limit
// (per GitHub docs), so this preflight is free.
const rateLimitURL = "https://api.github.com/rate_limit"

// authenticatedLimitFloor is the minimum rate-limit a healthy token returns.
// Anonymous = 60/hr; PAT = 5000/hr. Below this floor the token is degraded
// (revoked, scope-stripped, or unauthenticated).
const authenticatedLimitFloor = 1000

// remainingFloor is the minimum quota the agent must have before doing work.
// Below this, the agent retries (failed) instead of starting work that would
// inevitably 429.
const remainingFloor = 10

// ghTokenCheckStep verifies GH_TOKEN before each phase runs:
//   - empty token  → needs_input (human_review)
//   - 401          → needs_input (human_review) — token bad
//   - <1000/hr     → needs_input (human_review) — token degraded to anonymous
//   - <10 remaining→ failed (retryable, controller re-triggers after backoff)
//   - network err  → failed (retryable)
//   - healthy      → done + ContinueToNext (the next step runs)
type ghTokenCheckStep struct {
	token   string
	httpURL string // overridable for tests
	client  *http.Client
}

// NewGHTokenCheckStep constructs the preflight step. ghToken is the resolved
// token from env (passed by main → factory). Empty means the step fails fast
// on the first run.
func NewGHTokenCheckStep(ghToken string) agentlib.Step {
	return newGHTokenCheckStep(ghToken, rateLimitURL)
}

// newGHTokenCheckStep is the concrete constructor — exposed for tests
// (export_test.go) so they can inject a custom URL pointing at httptest.
func newGHTokenCheckStep(ghToken, httpURL string) *ghTokenCheckStep {
	return &ghTokenCheckStep{
		token:   ghToken,
		httpURL: httpURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Name implements agentlib.Step.
func (s *ghTokenCheckStep) Name() string { return "verify-gh-token" }

// ShouldRun always returns true — the check is cheap and the same token can
// become invalid mid-pipeline (revoked between phases).
func (s *ghTokenCheckStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run probes /rate_limit and returns Done+ContinueToNext on a healthy token,
// or a failure result on any degradation.
func (s *ghTokenCheckStep) Run(
	ctx context.Context,
	_ *agentlib.Markdown,
) (*agentlib.Result, error) {
	if s.token == "" {
		return needsInput("GH_TOKEN not set — agent cannot clone or inspect PRs"), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.httpURL, nil)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "build rate_limit request")
	}
	req.Header.Set("Authorization", "token "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return failed(fmt.Sprintf("GH rate_limit request failed (network): %v", err)), nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		return needsInput(fmt.Sprintf(
			"GH credentials unauthorized (HTTP 401) — rotate the token: %s",
			truncate(string(body)),
		)), nil
	}
	if resp.StatusCode != http.StatusOK {
		return failed(fmt.Sprintf(
			"GH rate_limit returned HTTP %d: %s",
			resp.StatusCode, truncate(string(body)),
		)), nil
	}

	var rl struct {
		Resources struct {
			Core struct {
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(body, &rl); err != nil {
		return failed(fmt.Sprintf("GH rate_limit response unparseable: %v", err)), nil
	}

	if rl.Resources.Core.Limit < authenticatedLimitFloor {
		return needsInput(fmt.Sprintf(
			"GH credentials authenticate as anonymous (limit=%d/hr, expected >=%d) — token revoked or scope-stripped",
			rl.Resources.Core.Limit,
			authenticatedLimitFloor,
		)), nil
	}

	if rl.Resources.Core.Remaining < remainingFloor {
		return failed(fmt.Sprintf(
			"GH rate limit nearly exhausted (%d/%d remaining) — retry after reset",
			rl.Resources.Core.Remaining, rl.Resources.Core.Limit,
		)), nil
	}

	return &agentlib.Result{
		Status:         agentlib.AgentStatusDone,
		ContinueToNext: true,
	}, nil
}

func needsInput(msg string) *agentlib.Result {
	return &agentlib.Result{
		Status:  agentlib.AgentStatusNeedsInput,
		Message: msg,
	}
}

func failed(msg string) *agentlib.Result {
	return &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: msg,
	}
}

// truncateMaxLen bounds diagnostic strings surfaced in escalation messages.
const truncateMaxLen = 200

func truncate(s string) string {
	if len(s) <= truncateMaxLen {
		return s
	}
	return s[:truncateMaxLen] + "..."
}
