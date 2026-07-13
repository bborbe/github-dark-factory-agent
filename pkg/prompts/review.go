// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/bborbe/errors"
)

//go:embed review.md
var reviewPrompt string

// ReviewOutcome values for ReviewVerdict.Outcome.
const (
	// ReviewOutcomePass means the PR diff plausibly implements the spec intent.
	ReviewOutcomePass = "pass"
	// ReviewOutcomeConcerns means a human must review before proceeding.
	ReviewOutcomeConcerns = "concerns"
)

// ReviewPrompt returns the embedded read-only diff-vs-spec review prompt. The
// caller concatenates the per-task context (repo, pr_number, matched spec
// paths) onto the returned string before invoking Claude.
func ReviewPrompt() string {
	return reviewPrompt
}

// ReviewVerdict is the typed shape of Claude's JSON response to the review
// prompt. Outcome is one of "pass" | "concerns"; Notes is a one-sentence
// justification.
type ReviewVerdict struct {
	Outcome string `json:"outcome"`
	Notes   string `json:"notes"`
}

// ParseReviewVerdict extracts a ReviewVerdict from Claude's raw output. Three
// extraction strategies are tried in order (mirroring the releaser's
// ParseBumpVerdict): plain JSON, fenced ```json block, last balanced {...}
// block. After unmarshal: Outcome MUST be one of {pass, concerns}; Notes MUST
// be non-empty.
//
// Errors are wrapped via github.com/bborbe/errors and always contain the
// literal substring "parse review verdict" so callers can grep verdict-parse
// failures apart from clone/git failures.
func ParseReviewVerdict(ctx context.Context, claudeOutput string) (ReviewVerdict, error) {
	trimmed := strings.TrimSpace(claudeOutput)

	var v ReviewVerdict

	// Strategy 1: parse the trimmed input as a JSON object directly.
	if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
		return validateReviewVerdict(ctx, v)
	}

	// Strategy 2: strip ```json fences.
	stripped := strings.TrimSpace(strings.TrimSuffix(
		strings.TrimPrefix(strings.TrimPrefix(trimmed, "```json"), "```"),
		"```",
	))
	if err := json.Unmarshal([]byte(stripped), &v); err == nil {
		return validateReviewVerdict(ctx, v)
	}

	// Strategy 3: find the last balanced {...} block in the input.
	block, ok := lastReviewJSONBlock(trimmed)
	if !ok {
		return ReviewVerdict{}, errors.Errorf(ctx, "parse review verdict: no JSON found")
	}
	if err := json.Unmarshal([]byte(block), &v); err != nil {
		return ReviewVerdict{}, errors.Wrapf(ctx, err, "parse review verdict: %s", block)
	}
	return validateReviewVerdict(ctx, v)
}

// validateReviewVerdict enforces the field-level invariants: Outcome must be in
// {pass, concerns}; Notes must be non-empty.
func validateReviewVerdict(ctx context.Context, v ReviewVerdict) (ReviewVerdict, error) {
	switch v.Outcome {
	case ReviewOutcomePass, ReviewOutcomeConcerns:
		// ok
	default:
		return ReviewVerdict{}, errors.Errorf(
			ctx,
			"parse review verdict: invalid outcome value %q (want pass|concerns)",
			v.Outcome,
		)
	}
	if strings.TrimSpace(v.Notes) == "" {
		return ReviewVerdict{}, errors.Errorf(ctx, "parse review verdict: missing notes")
	}
	return v, nil
}

// lastReviewJSONBlock returns the last balanced {...} substring in s, or
// "", false if none exists. Kept private to this package to avoid an unwanted
// dependency edge.
func lastReviewJSONBlock(s string) (string, bool) {
	end := strings.LastIndex(s, "}")
	if end < 0 {
		return "", false
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return s[i : end+1], true
			}
		}
	}
	return "", false
}
