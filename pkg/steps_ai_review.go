// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-dark-factory-agent/pkg/git"
	"github.com/bborbe/github-dark-factory-agent/pkg/prompts"
)

// reviewSectionHeading is the body section the ai_review phase produces. Its
// presence is the idempotency marker: a re-triggered ai_review phase with
// ## Review already written routes on the recorded verdict without re-running
// the (read-only but non-free) Claude review.
const reviewSectionHeading = "## Review"

// ReviewOutput is the typed contract for the ## Review section. Round-trips
// with agentlib.MarshalSectionTyped + agentlib.ExtractSection[ReviewOutput].
// Outcome records the verdict (prompts.ReviewOutcomePass /
// prompts.ReviewOutcomeConcerns) so a replay re-routes without re-reviewing.
type ReviewOutput struct {
	Repo         string   `json:"repo"`
	PRNumber     int      `json:"pr_number"`
	Outcome      string   `json:"outcome"`
	ChecksPassed []string `json:"checks_passed"`
	Notes        string   `json:"notes"`
}

// aiReviewStep implements agentlib.Step. It is the read-only verifier at the end
// of the lifecycle: it re-asserts the post-conditions deterministically (PR
// still draft, all matched specs completed, no prompts in-flight) then runs a
// read-only Claude diff-vs-spec sanity check. On a passing verdict it writes
// ## Review and routes to human_review (the human's draft→ready flip is the
// sign-off); on blocking concerns or any post-condition failure it escalates
// (Status: failed) per the escalation doctrine — it NEVER runs `gh pr ready`,
// NEVER mutates assignee/status (the controller clears assignee when
// phase == human_review).
type aiReviewStep struct {
	github      GitHubClient
	repoManager git.RepoManager
	runner      claudelib.ClaudeRunner
}

// NewAIReviewStep wires the ai_review step with its three IO seams: the GitHub
// REST client (PR draft flag), the repo manager (worktree for the spec /
// prompt-queue inspection), and the read-only Claude runner (diff-vs-spec
// sanity). The runner is injected so tests can fake the verdict without a real
// claude — mirroring how the execution step injects ExecutionRunner.
func NewAIReviewStep(
	github GitHubClient,
	repoManager git.RepoManager,
	runner claudelib.ClaudeRunner,
) agentlib.Step {
	return &aiReviewStep{github: github, repoManager: repoManager, runner: runner}
}

// Name implements agentlib.Step.
func (s *aiReviewStep) Name() string { return "df-ai-review" }

// ShouldRun always returns true. Idempotency lives INSIDE Run (## Review
// present → re-route on the recorded verdict) so the routing decision is never
// silently skipped.
func (s *aiReviewStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run re-asserts the post-conditions, runs the read-only Claude review, writes
// ## Review, and routes: pass → Done + human_review; concerns → failed
// (escalate). Deterministic post-condition failures escalate WITHOUT writing
// ## Review (they are pre-checks, not a verdict).
func (s *aiReviewStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	// Idempotency: a prior run already produced ## Review → re-route on the
	// recorded verdict without re-reviewing.
	if _, ok := md.FindSection(reviewSectionHeading); ok {
		return s.routeRecorded(ctx, md)
	}

	plan, err := agentlib.ExtractSection[PlanOutput](ctx, md, planSectionHeading)
	if err != nil {
		return failed("ai_review: read " + planSectionHeading + ": " + err.Error()), nil
	}
	if _, err := agentlib.ExtractSection[ExecutionOutput](ctx, md, resultSectionHeading); err != nil {
		return failed("ai_review: read " + resultSectionHeading + ": " + err.Error()), nil
	}

	fields, missing := reviewFrontmatter(md)
	if missing != "" {
		return needsInput("ai_review: required frontmatter field missing: " + missing), nil
	}
	prNumber, ok := parsePRNumber(md, fields["pr_number"])
	if !ok {
		return needsInput(
			"ai_review: frontmatter pr_number is not a valid integer: " + fields["pr_number"],
		), nil
	}
	owner, name, ok := parseOwnerRepo(fields["repo"])
	if !ok {
		return needsInput(
			`ai_review: frontmatter "repo" must be "owner/name"; got ` + fields["repo"],
		), nil
	}

	return s.review(ctx, md, plan, fields, prNumber, owner, name)
}

// review runs the deterministic post-condition gate, the Claude verdict, and
// the routing.
func (s *aiReviewStep) review(
	ctx context.Context,
	md *agentlib.Markdown,
	plan *PlanOutput,
	fields map[string]string,
	prNumber int,
	owner, name string,
) (*agentlib.Result, error) {
	// (a) PR must STILL be a draft. If a human already flipped it to ready this
	// agent must not proceed — the human took over.
	pr, err := s.github.GetPullRequest(ctx, owner, name, prNumber)
	if err != nil {
		return failed(
			fmt.Sprintf("ai_review: fetch PR %s/%s#%d: %v", owner, name, prNumber, err),
		), nil
	}
	if !pr.Draft {
		return failed(fmt.Sprintf(
			"ai_review: PR #%d is no longer a draft (human already flipped it)", prNumber,
		)), nil
	}

	worktree, err := s.repoManager.EnsureWorktree(
		ctx, fields["clone_url"], fields["ref"], fields["task_identifier"],
	)
	if err != nil {
		if git.IsGitAuthFailure(err) {
			return needsInput(
				"ai_review: clone failed (authentication required — set GH_TOKEN and re-trigger)",
			), nil
		}
		return failed("ai_review: ensure worktree: " + err.Error()), nil
	}

	// (b) every matched spec must now be completed.
	specIDs := specIDsFromPaths(plan.MatchedSpecs)
	if incomplete := firstIncompleteSpec(ctx, worktree, specIDs); incomplete != "" {
		return failed(fmt.Sprintf(
			"ai_review: spec %s is not completed (execution did not finish)", incomplete,
		)), nil
	}

	// (c) no prompts may be left in-flight.
	if hasInProgressPrompts(worktree) {
		return failed(
			"ai_review: prompts still in prompts/in-progress (execution did not drain the queue)",
		), nil
	}

	// (d) read-only Claude diff-vs-spec sanity → verdict.
	verdict := s.claudeVerdict(ctx, fields["repo"], prNumber, plan.MatchedSpecs)
	return s.writeAndRoute(ctx, md, plan.Repo, prNumber, verdict)
}

// claudeVerdict runs the read-only review prompt and parses the structured
// verdict. Any runner or parse failure fails closed: it returns a concerns
// verdict so the human triages rather than the agent falsely passing.
func (s *aiReviewStep) claudeVerdict(
	ctx context.Context,
	repo string,
	prNumber int,
	matchedSpecs []string,
) prompts.ReviewVerdict {
	prompt := prompts.ReviewPrompt() +
		"\n\n## Context\n\n" +
		"Repo: " + repo + "\n" +
		"PR number: " + strconv.Itoa(prNumber) + "\n" +
		"Matched specs:\n" + strings.Join(matchedSpecs, "\n") + "\n"

	result, err := s.runner.Run(ctx, prompt)
	if err != nil {
		glog.Warningf("ai_review: claude review call failed: %v", err)
		return prompts.ReviewVerdict{
			Outcome: prompts.ReviewOutcomeConcerns,
			Notes:   "claude review unavailable: " + truncate(err.Error()),
		}
	}
	verdict, err := prompts.ParseReviewVerdict(ctx, result.Result)
	if err != nil {
		glog.Warningf("ai_review: parse review verdict failed: %v", err)
		return prompts.ReviewVerdict{
			Outcome: prompts.ReviewOutcomeConcerns,
			Notes:   "unparseable review verdict: " + truncate(err.Error()),
		}
	}
	return verdict
}

// writeAndRoute writes ## Review (vault-first) then routes on the verdict:
// pass → Done + human_review; concerns → failed (escalate). Assignee/status are
// never touched — the controller clears assignee when phase == human_review.
func (s *aiReviewStep) writeAndRoute(
	ctx context.Context,
	md *agentlib.Markdown,
	repo string,
	prNumber int,
	verdict prompts.ReviewVerdict,
) (*agentlib.Result, error) {
	output := ReviewOutput{
		Repo:     repo,
		PRNumber: prNumber,
		Outcome:  verdict.Outcome,
		Notes:    verdict.Notes,
	}
	if verdict.Outcome == prompts.ReviewOutcomePass {
		output.ChecksPassed = []string{
			"pr_still_draft",
			"specs_completed",
			"no_prompts_in_flight",
			"diff_matches_spec_intent",
		}
	}
	section, err := agentlib.MarshalSectionTyped(ctx, reviewSectionHeading, output)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Review section")
	}
	md.ReplaceSection(section)

	if verdict.Outcome == prompts.ReviewOutcomePass {
		glog.V(2).Infof("ai_review: PASS for %s#%d — routing to human_review", repo, prNumber)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: string(domain.TaskPhaseHumanReview),
		}, nil
	}
	glog.V(2).Infof("ai_review: CONCERNS for %s#%d — escalating: %s", repo, prNumber, verdict.Notes)
	return failed("ai_review: review surfaced blocking concerns: " + verdict.Notes), nil
}

// routeRecorded is the idempotency path: ## Review already exists → re-route on
// the recorded verdict without re-running the review. A recorded pass routes to
// human_review; anything else escalates.
func (s *aiReviewStep) routeRecorded(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	output, err := agentlib.ExtractSection[ReviewOutput](ctx, md, reviewSectionHeading)
	if err != nil {
		return failed("ai_review: read recorded " + reviewSectionHeading + ": " + err.Error()), nil
	}
	if output.Outcome == prompts.ReviewOutcomePass {
		glog.V(2).
			Infof("ai_review: %s already present (pass) — routing to human_review", reviewSectionHeading)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: string(domain.TaskPhaseHumanReview),
		}, nil
	}
	glog.V(2).
		Infof("ai_review: %s already present (%s) — re-escalating", reviewSectionHeading, output.Outcome)
	return failed("ai_review: recorded review surfaced blocking concerns: " + output.Notes), nil
}

// reviewFrontmatter reads the frontmatter fields the ai_review step needs.
// Returns the first missing field's name ("" if all present) and the values.
func reviewFrontmatter(md *agentlib.Markdown) (map[string]string, string) {
	keys := []string{"repo", "pr_number", "clone_url", "ref", "task_identifier"}
	values := map[string]string{}
	for _, key := range keys {
		v, _ := md.Frontmatter.String(key)
		if strings.TrimSpace(v) == "" {
			// pr_number may be a YAML int — recover it before declaring missing.
			if key == "pr_number" {
				if n, ok := md.Frontmatter.Int(key); ok {
					values[key] = strconv.Itoa(n)
					continue
				}
			}
			return values, key
		}
		values[key] = v
	}
	return values, ""
}

// firstIncompleteSpec returns the id of the first spec that is NOT completed, or
// "" when every spec is completed. A spec is completed when it has moved to
// specs/completed/<id>.md OR its specs/in-progress/<id>.md carries frontmatter
// completed:true (reusing the frontmatterTruthy contract from planning).
func firstIncompleteSpec(ctx context.Context, worktree string, specIDs []string) string {
	for _, id := range specIDs {
		if !specCompleted(ctx, worktree, id) {
			return id
		}
	}
	return ""
}

// specCompleted reports whether spec <id> reached the completed terminal state.
func specCompleted(ctx context.Context, worktree, id string) bool {
	if fileExists(filepath.Join(worktree, "specs", "completed", id+".md")) {
		return true
	}
	path := filepath.Join(worktree, "specs", "in-progress", id+".md")
	content, err := os.ReadFile(path) // #nosec G304 -- path from validated worktree + spec id
	if err != nil {
		return false
	}
	parsed, err := agentlib.ParseMarkdown(ctx, string(content))
	if err != nil {
		return false
	}
	return frontmatterTruthy(parsed.Frontmatter, "completed")
}
