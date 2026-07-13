// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-dark-factory-agent/pkg/git"
)

// resultSectionHeading is the body section the execution phase produces. Its
// presence is the idempotency marker: a re-triggered execution phase with
// ## Result already written routes straight to ai_review without re-running
// the (expensive, side-effecting) dark-factory lifecycle.
const resultSectionHeading = "## Result"

// executionFlags are the per-invocation dark-factory config overrides the
// execution step passes to the lifecycle runner. They are supplied via --set /
// dedicated CLI flags and NEVER written to the branch — a committed
// .dark-factory.yaml divergence conflicts workflow:direct's `git merge
// origin/master` (design Part 5).
//
//   - backend=local           run claude in-process, no nested docker (no DinD)
//   - autoGeneratePrompts=true generate prompts from the approved spec
//   - --auto-approve-prompts   audit + approve each generated prompt headlessly
//   - --skip-preflight         the pod is the sandbox; no baseline gate needed
//   - --skip-healthcheck       claude auth is verified by the claude-auth preflight step
//
// Accepted risk — --auto-approve-prompts blast radius. This flag does NOT
// blindly approve generated prompts: it triggers dark-factory's spec-078
// audit-then-approve, so each auto-generated prompt is AUDITED headlessly and
// only approved if the audit passes (fail-closed — a failing audit stops the
// spec, and the execution step escalates). This is the intended bookend design:
// the human pre-approves the SPEC on the draft PR before the agent ever runs, so
// what gets auto-approved is bounded to prompts derived from a human-approved
// spec. The blast radius is bounded by four independent gates:
//
//	(a) the human pre-approving the SPEC on the draft PR (first bookend);
//	(b) the spec-078 fail-closed audit gate on every generated prompt;
//	(c) this agent's ai_review diff-vs-spec sanity check before human_review;
//	(d) the human's draft→ready flip on the PR (final bookend).
//
// The execution pod is single-tenant and ephemeral, so a rogue prompt cannot
// reach beyond this one PR branch. dark-factory has no per-prompt-id allowlist
// (--auto-approve-prompt-ids does not exist); the audit gate IS the allowlist.
var executionFlags = []string{
	"--set", "backend=local",
	"--set", "autoGeneratePrompts=true",
	"--auto-approve-prompts",
	"--skip-preflight",
	"--skip-healthcheck",
}

// specStatusVerifying / specStatusCompleted are the two terminal-ish spec
// states the execution step accepts after the lifecycle drains. Any other
// state means a prompt failed DoD/audit (spec-078 fail-closed) and the spec
// never reached verification — the step escalates.
const (
	specStatusVerifying = "verifying"
	specStatusCompleted = "completed"
)

// LifecycleResult reports what one dark-factory lifecycle run produced. The
// production runner derives it by reading spec frontmatter after the prompt
// queue drains; the step turns it into ## Result and the spec-complete /
// push follow-up.
type LifecycleResult struct {
	// PromptsExecuted is the number of prompts dark-factory completed this run.
	PromptsExecuted int
	// SpecStatuses maps each requested spec id to its status after the queue
	// drained (e.g. "verifying", "completed").
	SpecStatuses map[string]string
}

// ExecutionRunner is the injectable seam over the dark-factory lifecycle and
// the git push. The production impl shells `dark-factory` / `git` via os/exec
// with the worktree as cwd, inheriting the pod env (HOME for claude auth,
// GH_TOKEN for push). Faking it lets the step's routing / escalation /
// idempotency logic be unit-tested without a real claude or network.
//
//counterfeiter:generate -o ../mocks/execution-runner.go --fake-name ExecutionRunner . ExecutionRunner
type ExecutionRunner interface {
	// RunLifecycle drives generate → auto-approve → execute → auto-verify for
	// the given specs in workdir with backend:local (flags), blocking until the
	// prompt queue drains, then returns the per-spec status. It never pushes
	// (autoRelease:false) — the step drives the push separately.
	RunLifecycle(
		ctx context.Context,
		workdir string,
		specIDs, flags []string,
	) (*LifecycleResult, error)
	// CompleteSpec drives `dark-factory spec complete <specID>` (verifying →
	// completed). Required because workflow:direct only auto-marks the spec
	// `verifying`; without this the watcher re-emits the task forever.
	CompleteSpec(ctx context.Context, workdir, specID string) error
	// PushBranch pushes the per-prompt commits on HEAD to origin/<branch>.
	PushBranch(ctx context.Context, workdir, branch string) error
}

// ExecutionOutput is the typed contract for the ## Result section. Round-trips
// with agentlib.MarshalSectionTyped + agentlib.ExtractSection[ExecutionOutput].
type ExecutionOutput struct {
	Repo            string   `json:"repo"`
	Branch          string   `json:"branch"`
	SpecsCompleted  []string `json:"specs_completed"`
	PromptsExecuted int      `json:"prompts_executed"`
	CommitsPushed   bool     `json:"commits_pushed"`
}

// executionStep implements agentlib.Step. It clones the draft-PR branch's
// worktree, drives the dark-factory backend:local lifecycle end to end,
// completes the spec, pushes the per-prompt commits, writes ## Result, and
// routes to ai_review.
type executionStep struct {
	repoManager git.RepoManager
	runner      ExecutionRunner
}

// NewExecutionStep wires the execution step with its two IO seams: the repo
// manager (worktree) and the dark-factory/git lifecycle runner.
func NewExecutionStep(repoManager git.RepoManager, runner ExecutionRunner) agentlib.Step {
	return &executionStep{repoManager: repoManager, runner: runner}
}

// Name implements agentlib.Step.
func (s *executionStep) Name() string { return "df-execution" }

// ShouldRun always returns true. Idempotency lives INSIDE Run (## Result
// present → re-route) so the routing decision is never silently skipped —
// putting it in ShouldRun would short-circuit the phase to done.
func (s *executionStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run drives the dark-factory lifecycle and routes to ai_review, or returns a
// failure result per the escalation doctrine (no assignee/status mutation, no
// ## Failure, no auto-fix loop).
func (s *executionStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	// Idempotency: a prior run already produced ## Result → re-route without
	// re-running the (expensive, side-effecting) lifecycle.
	if _, ok := md.FindSection(resultSectionHeading); ok {
		glog.V(2).
			Infof("execution: %s already present — routing to ai_review", resultSectionHeading)
		return advanceToAIReview(), nil
	}

	plan, err := agentlib.ExtractSection[PlanOutput](ctx, md, planSectionHeading)
	if err != nil {
		return failed("execution: read " + planSectionHeading + ": " + err.Error()), nil
	}
	if len(plan.MatchedSpecs) == 0 {
		return failed(
			"execution: " + planSectionHeading + " lists no specs — planning did not run",
		), nil
	}

	cloneURL, _ := md.Frontmatter.String("clone_url")
	ref, _ := md.Frontmatter.String("ref")
	branch, _ := md.Frontmatter.String("branch")
	taskID, _ := md.Frontmatter.String("task_identifier")
	if missing := firstEmpty(map[string]string{
		"clone_url":       cloneURL,
		"ref":             ref,
		"branch":          branch,
		"task_identifier": taskID,
	}); missing != "" {
		return needsInput("execution: required frontmatter field missing: " + missing), nil
	}

	return s.runLifecycle(ctx, md, plan, lifecycleParams{
		cloneURL: cloneURL,
		ref:      ref,
		branch:   branch,
		taskID:   taskID,
	})
}

// lifecycleParams carries the frontmatter-derived inputs for one execution run.
type lifecycleParams struct {
	cloneURL string
	ref      string
	branch   string
	taskID   string
}

// runLifecycle ensures the worktree, drives dark-factory, completes the spec,
// pushes, writes ## Result, and routes.
func (s *executionStep) runLifecycle(
	ctx context.Context,
	md *agentlib.Markdown,
	plan *PlanOutput,
	p lifecycleParams,
) (*agentlib.Result, error) {
	worktree, err := s.repoManager.EnsureWorktree(ctx, p.cloneURL, p.ref, p.taskID)
	if err != nil {
		if git.IsGitAuthFailure(err) {
			return needsInput(
				"execution: clone failed (authentication required — set GH_TOKEN and re-trigger)",
			), nil
		}
		return failed("execution: ensure worktree: " + err.Error()), nil
	}

	specIDs := specIDsFromPaths(plan.MatchedSpecs)
	result, err := s.runner.RunLifecycle(ctx, worktree, specIDs, executionFlags)
	if err != nil {
		// A dark-factory / DoD / audit failure (spec-078 fail-closed). Escalate;
		// NO auto-fix loop, NO assignee mutation.
		return failed("execution: dark-factory lifecycle failed: " + err.Error()), nil
	}

	completed, escalation := s.completeSpecs(ctx, worktree, specIDs, result)
	if escalation != nil {
		return escalation, nil
	}

	if err := s.runner.PushBranch(ctx, worktree, p.branch); err != nil {
		return failed("execution: push branch " + p.branch + ": " + err.Error()), nil
	}

	// Vault-first: write ## Result BEFORE routing.
	output := ExecutionOutput{
		Repo:            plan.Repo,
		Branch:          p.branch,
		SpecsCompleted:  completed,
		PromptsExecuted: result.PromptsExecuted,
		CommitsPushed:   true,
	}
	section, err := agentlib.MarshalSectionTyped(ctx, resultSectionHeading, output)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Result section")
	}
	md.ReplaceSection(section)

	glog.V(2).Infof(
		"execution: wrote %s for %s specs=%v prompts=%d",
		resultSectionHeading, plan.Repo, completed, result.PromptsExecuted,
	)
	return advanceToAIReview(), nil
}

// completeSpecs drives `dark-factory spec complete` for every matched spec left
// in the `verifying` state, tolerating specs that already auto-completed. It
// returns the completed spec ids, or an escalation result when any spec never
// reached a terminal state (a prompt failed DoD/audit — spec-078 fail-closed).
func (s *executionStep) completeSpecs(
	ctx context.Context,
	worktree string,
	specIDs []string,
	result *LifecycleResult,
) ([]string, *agentlib.Result) {
	completed := make([]string, 0, len(specIDs))
	for _, specID := range specIDs {
		status := strings.TrimSpace(result.SpecStatuses[specID])
		switch status {
		case specStatusCompleted:
			completed = append(completed, specID)
		case specStatusVerifying:
			if err := s.runner.CompleteSpec(ctx, worktree, specID); err != nil {
				return nil, failed(
					fmt.Sprintf("execution: spec complete %s: %v", specID, err),
				)
			}
			completed = append(completed, specID)
		default:
			// Not verifying/completed → a prompt failed DoD or audit; the spec
			// never reached verification. Fail closed, no auto-fix.
			return nil, failed(fmt.Sprintf(
				"execution: spec %s did not reach verification (status=%q) — prompt failed DoD/audit",
				specID,
				status,
			))
		}
	}
	return completed, nil
}

// advanceToAIReview is the success terminal for the execution phase.
func advanceToAIReview() *agentlib.Result {
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: string(domain.TaskPhaseAIReview),
	}
}

// specIDsFromPaths turns repo-relative spec paths ("specs/in-progress/001-x.md")
// into dark-factory spec ids ("001-x") suitable for `spec complete`.
func specIDsFromPaths(paths []string) []string {
	ids := make([]string, 0, len(paths))
	for _, p := range paths {
		base := filepath.Base(p)
		ids = append(ids, strings.TrimSuffix(base, filepath.Ext(base)))
	}
	return ids
}

// firstEmpty returns the first key whose value is empty (after trimming), or ""
// when all values are non-empty. Iteration order is deterministic across the
// fixed key set only for the message; any missing field is a task-body defect.
func firstEmpty(fields map[string]string) string {
	// Deterministic order for a stable "first missing wins" message.
	for _, key := range []string{"clone_url", "ref", "branch", "task_identifier"} {
		if strings.TrimSpace(fields[key]) == "" {
			return key
		}
	}
	return ""
}
