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
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-dark-factory-agent/pkg/git"
)

// AgentLogin is this agent's task-system identity. Kept as a named constant so
// tests and future escalation code reference one source of truth.
const AgentLogin = "github-dark-factory-agent"

// planSectionHeading is the body section the planning phase produces.
const planSectionHeading = "## Plan"

// darkFactoryConfigFile is the per-repo config the watcher requires on the
// draft-PR branch (validated present as a precondition).
const darkFactoryConfigFile = ".dark-factory.yaml"

// specGlobDir is the directory (relative to repo root) holding in-progress
// specs. Only specs here that also appear in the PR diff and are
// approved-but-not-completed satisfy the planning gate.
const specGlobDir = "specs/in-progress"

// planningRequiredFields are the frontmatter keys the planning step reads
// before any IO. Order fixes the deterministic "first missing wins" message.
var planningRequiredFields = []string{
	"clone_url",
	"ref",
	"branch",
	"pr_number",
	"task_identifier",
	"repo",
}

// PlanOutput is the typed contract for the ## Plan section. Round-trips with
// agentlib.MarshalSectionTyped + agentlib.ExtractSection[PlanOutput].
type PlanOutput struct {
	Repo         string   `json:"repo"`
	PRNumber     int      `json:"pr_number"`
	HeadSHA      string   `json:"head_sha"`
	MatchedSpecs []string `json:"matched_specs"`
	ChecksPassed []string `json:"checks_passed"`
}

// planningStep implements agentlib.Step. It is pure Go — no Claude. It clones
// the draft-PR branch and validates the preconditions that the watcher's
// coarse emit-time check cannot verify, then writes ## Plan and routes to
// execution.
type planningStep struct {
	repoManager git.RepoManager
	github      GitHubClient
}

// NewPlanningStep wires the planning step with its two IO seams: the repo
// manager (bare-clone cache + worktree) and the GitHub REST client (PR draft
// flag, head SHA, changed files).
func NewPlanningStep(repoManager git.RepoManager, github GitHubClient) agentlib.Step {
	return &planningStep{repoManager: repoManager, github: github}
}

// Name implements agentlib.Step.
func (s *planningStep) Name() string { return "df-planning" }

// ShouldRun always returns true. Idempotency lives INSIDE Run (## Plan present
// → re-route) so the routing decision is never silently skipped.
func (s *planningStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run validates the planning preconditions and either writes ## Plan +
// routes to execution, or returns Failed/NeedsInput per the escalation
// doctrine (no assignee/status mutation, no ## Failure section).
func (s *planningStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	// Idempotency: a prior run already produced ## Plan → re-route without
	// re-cloning or re-validating.
	if _, ok := md.FindSection(planSectionHeading); ok {
		glog.V(2).Infof("planning: %s already present — routing to execution", planSectionHeading)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: string(domain.TaskPhaseExecution),
		}, nil
	}

	fields, missing := readRequiredFields(md, planningRequiredFields)
	if missing != "" {
		// Missing frontmatter is a task-body problem — retrying won't help.
		return needsInput("planning: required frontmatter field missing: " + missing), nil
	}

	prNumber, ok := parsePRNumber(md, fields["pr_number"])
	if !ok {
		return needsInput(
			"planning: frontmatter pr_number is not a valid integer: " + fields["pr_number"],
		), nil
	}
	owner, name, ok := parseOwnerRepo(fields["repo"])
	if !ok {
		return needsInput(
			`planning: frontmatter "repo" must be "owner/name"; got ` + fields["repo"],
		), nil
	}

	return s.validateAndPlan(ctx, md, fields, prNumber, owner, name)
}

// validateAndPlan clones the branch, runs the four preconditions, and either
// writes ## Plan (routing to execution) or returns the escalation result.
func (s *planningStep) validateAndPlan(
	ctx context.Context,
	md *agentlib.Markdown,
	fields map[string]string,
	prNumber int,
	owner, name string,
) (*agentlib.Result, error) {
	worktree, err := s.repoManager.EnsureWorktree(
		ctx,
		fields["clone_url"],
		fields["ref"],
		fields["task_identifier"],
	)
	if err != nil {
		return failed("planning: ensure worktree: " + err.Error()), nil
	}

	pr, err := s.github.GetPullRequest(ctx, owner, name, prNumber)
	if err != nil {
		return failed(
			fmt.Sprintf("planning: fetch PR %s/%s#%d: %v", owner, name, prNumber, err),
		), nil
	}

	// (a) ref must equal the PR head SHA — the watcher emits the SHA it saw;
	// a mismatch means the branch advanced under us.
	if pr.HeadSHA != fields["ref"] {
		return failed(fmt.Sprintf(
			"planning: ref %q != PR #%d head sha %q (branch advanced since emit)",
			fields["ref"], prNumber, pr.HeadSHA,
		)), nil
	}

	// (d) PR must be a draft — this agent never operates on a ready PR.
	if !pr.Draft {
		return failed(fmt.Sprintf("planning: PR #%d is not a draft", prNumber)), nil
	}

	// (b) .dark-factory.yaml must be present on the branch.
	if !fileExists(filepath.Join(worktree, darkFactoryConfigFile)) {
		return failed("planning: " + darkFactoryConfigFile + " missing in worktree"), nil
	}

	// (c) at least one approved-not-completed spec under specs/in-progress/
	// that also appears in the PR diff.
	prFiles, err := s.github.ListPullRequestFiles(ctx, owner, name, prNumber)
	if err != nil {
		return failed(fmt.Sprintf("planning: list PR #%d files: %v", prNumber, err)), nil
	}
	matched, err := matchApprovedSpecs(ctx, worktree, prFiles)
	if err != nil {
		return failed("planning: scan specs: " + err.Error()), nil
	}
	if len(matched) == 0 {
		return failed(
			"planning: no approved-not-completed spec under " + specGlobDir + "/ found in PR #" +
				strconv.Itoa(prNumber) + " diff",
		), nil
	}

	output := PlanOutput{
		Repo:         fields["repo"],
		PRNumber:     prNumber,
		HeadSHA:      fields["ref"],
		MatchedSpecs: matched,
		ChecksPassed: []string{
			"ref_matches_head",
			"pr_is_draft",
			"dark_factory_yaml_present",
			"approved_spec_in_diff",
		},
	}
	section, err := agentlib.MarshalSectionTyped(ctx, planSectionHeading, output)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Plan section")
	}
	md.ReplaceSection(section)

	glog.V(2).
		Infof("planning: wrote %s for %s#%d specs=%v", planSectionHeading, fields["repo"], prNumber, matched)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: string(domain.TaskPhaseExecution),
	}, nil
}

// readRequiredFields pulls the required frontmatter fields. Returns the first
// missing field's name ("" if all present) and the resolved values. An empty
// string counts as missing.
func readRequiredFields(md *agentlib.Markdown, keys []string) (map[string]string, string) {
	values := map[string]string{}
	for _, key := range keys {
		v, _ := md.Frontmatter.String(key)
		if strings.TrimSpace(v) == "" {
			// pr_number may be a YAML int — String() returns ok=false for
			// non-strings; recover it before declaring it missing.
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

// parsePRNumber resolves pr_number from either a YAML int or a string field.
func parsePRNumber(md *agentlib.Markdown, raw string) (int, bool) {
	if n, ok := md.Frontmatter.Int("pr_number"); ok {
		return n, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseOwnerRepo splits an "owner/name" string. Empty or no-slash input
// returns ok=false.
func parseOwnerRepo(s string) (owner, name string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// matchApprovedSpecs returns the repo-relative paths of specs under
// specs/in-progress/ that (i) appear in prFiles AND (ii) carry frontmatter
// approved-set + completed-unset.
func matchApprovedSpecs(ctx context.Context, worktree string, prFiles []string) ([]string, error) {
	prFileSet := make(map[string]struct{}, len(prFiles))
	for _, f := range prFiles {
		prFileSet[f] = struct{}{}
	}

	matches, err := filepath.Glob(filepath.Join(worktree, specGlobDir, "*.md"))
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "glob %s", specGlobDir)
	}

	var matched []string
	for _, absPath := range matches {
		relPath := specGlobDir + "/" + filepath.Base(absPath)
		if _, inDiff := prFileSet[relPath]; !inDiff {
			continue
		}
		approved, err := specApprovedNotCompleted(ctx, absPath)
		if err != nil {
			return nil, err
		}
		if approved {
			matched = append(matched, relPath)
		}
	}
	return matched, nil
}

// specApprovedNotCompleted parses a spec file's frontmatter and reports
// whether `approved` is set (truthy) and `completed` is unset (falsy/absent).
func specApprovedNotCompleted(ctx context.Context, path string) (bool, error) {
	content, err := os.ReadFile(path) // #nosec G304 -- path from Glob over the validated worktree
	if err != nil {
		return false, errors.Wrapf(ctx, err, "read spec %s", path)
	}
	parsed, err := agentlib.ParseMarkdown(ctx, string(content))
	if err != nil {
		return false, errors.Wrapf(ctx, err, "parse spec %s", path)
	}
	return frontmatterTruthy(parsed.Frontmatter, "approved") &&
		!frontmatterTruthy(parsed.Frontmatter, "completed"), nil
}

// frontmatterTruthy reports whether key holds a set/truthy value. A bool
// yields its value; a string is truthy when non-empty and not "false"; any
// other non-nil value (date, number) is truthy; absent/nil is false.
func frontmatterTruthy(fm agentlib.TaskFrontmatter, key string) bool {
	v, ok := fm[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		trimmed := strings.TrimSpace(t)
		return trimmed != "" && !strings.EqualFold(trimmed, "false")
	default:
		return true
	}
}

// fileExists reports whether path exists (any file type).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
