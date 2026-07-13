// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/mocks"
	"github.com/bborbe/github-dark-factory-agent/pkg"
)

// reviewTask builds an execution-complete task: full frontmatter plus ## Plan
// (one matched spec) and ## Result, which is what the ai_review phase consumes.
func reviewTask() string {
	return `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: ai_review
repo: bborbe/sandbox
clone_url: https://github.com/bborbe/sandbox.git
ref: abc123
branch: feature/x
pr_number: 7
task_identifier: ` + validTaskID + `
---

# Dark-Factory Implement

## Plan

` + "```json" + `
{"repo":"bborbe/sandbox","pr_number":7,"head_sha":"abc123","matched_specs":["specs/in-progress/001-hello.md"],"checks_passed":["approved_spec_in_diff"]}
` + "```" + `

## Result

` + "```json" + `
{"repo":"bborbe/sandbox","branch":"feature/x","specs_completed":["001-hello"],"prompts_executed":1,"commits_pushed":true}
` + "```" + `
`
}

// completedWorktree builds a worktree where the matched spec has moved to
// specs/completed/ and no prompts remain in prompts/in-progress/.
func completedWorktree() string {
	dir := GinkgoT().TempDir()
	completedDir := filepath.Join(dir, "specs", "completed")
	Expect(os.MkdirAll(completedDir, 0750)).To(BeNil())
	spec := "---\nkind: feature\napproved: true\ncompleted: true\n---\n\nSpec body.\n"
	Expect(
		os.WriteFile(filepath.Join(completedDir, "001-hello.md"), []byte(spec), 0600),
	).To(BeNil())
	return dir
}

var _ = Describe("AIReviewStep", func() {
	var (
		ctx        context.Context
		fakeGH     *mocks.GitHubClient
		fakeRepo   *mocks.RepoManager
		fakeRunner *mocks.ClaudeRunner
		worktree   string
		step       agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		fakeGH = &mocks.GitHubClient{}
		fakeRepo = &mocks.RepoManager{}
		fakeRunner = &mocks.ClaudeRunner{}
		worktree = completedWorktree()
		fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: true, HeadSHA: "abc123"}, nil)
		fakeRepo.EnsureWorktreeReturns(worktree, nil)
		step = pkg.NewAIReviewStep(fakeGH, fakeRepo, fakeRunner)
	})

	run := func(taskContent string) (*agentlib.Result, *agentlib.Markdown) {
		md, err := agentlib.ParseMarkdown(ctx, taskContent)
		Expect(err).To(BeNil())
		result, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		return result, md
	}

	// assertNoReadySignal asserts the escalation doctrine on any failure path:
	// assignee + status untouched (the controller owns the envelope).
	assertUntouched := func(md *agentlib.Markdown) {
		assignee, _ := md.Frontmatter.String("assignee")
		Expect(assignee).To(Equal("github-dark-factory-agent"))
		status, _ := md.Frontmatter.String("status")
		Expect(status).To(Equal("in_progress"))
	}

	Describe("happy path (draft + specs completed + no in-flight + claude pass)", func() {
		BeforeEach(func() {
			fakeRunner.RunReturns(&claudelib.ClaudeResult{
				Result: `{"outcome":"pass","notes":"diff implements the spec"}`,
			}, nil)
		})

		It("writes ## Review and routes to human_review", func() {
			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))

			out, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(out.Outcome).To(Equal("pass"))
			Expect(out.Repo).To(Equal("bborbe/sandbox"))
			Expect(out.PRNumber).To(Equal(7))
			Expect(out.ChecksPassed).To(ContainElement("diff_matches_spec_intent"))

			// Read-only review actually ran; PR draft flag was checked.
			Expect(fakeRunner.RunCallCount()).To(Equal(1))
			Expect(fakeGH.GetPullRequestCallCount()).To(Equal(1))
		})

		It("does NOT clear the assignee (controller does when phase==human_review)", func() {
			_, md := run(reviewTask())
			assertUntouched(md)
		})
	})

	Describe("idempotency", func() {
		It("routes to human_review on a recorded pass without re-running", func() {
			task := reviewTask() +
				"\n## Review\n\n```json\n{\"repo\":\"bborbe/sandbox\",\"pr_number\":7,\"outcome\":\"pass\",\"notes\":\"ok\"}\n```\n"
			result, _ := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(fakeGH.GetPullRequestCallCount()).To(Equal(0))
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
		})

		It("re-escalates on a recorded concerns without re-running", func() {
			task := reviewTask() +
				"\n## Review\n\n```json\n{\"repo\":\"bborbe/sandbox\",\"pr_number\":7,\"outcome\":\"concerns\",\"notes\":\"needs eyes\"}\n```\n"
			result, _ := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
		})

		It("re-escalates on a recorded non-pass verdict WITHOUT touching assignee/status", func() {
			// The routeRecorded idempotency path must honour the escalation
			// doctrine: a recorded outcome != pass re-escalates (failed) and leaves
			// the controller-owned envelope (assignee + status) untouched.
			task := reviewTask() +
				"\n## Review\n\n```json\n{\"repo\":\"bborbe/sandbox\",\"pr_number\":7,\"outcome\":\"concerns\",\"notes\":\"needs eyes\"}\n```\n"
			result, md := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("blocking concerns"))
			assertUntouched(md)
			// No IO happened on the recorded path.
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
			Expect(fakeGH.GetPullRequestCallCount()).To(Equal(0))
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
		})
	})

	Describe("escalation paths", func() {
		It("fails when the PR is no longer a draft (human already flipped it)", func() {
			fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: false, HeadSHA: "abc123"}, nil)
			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("no longer a draft"))
			assertUntouched(md)
			_, ok := md.FindSection("## Review")
			Expect(ok).To(BeFalse())
			// Never proceeds to the review / worktree once the PR is ready.
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
		})

		It("fails (no ## Review) when the pre-verdict PR fetch errors", func() {
			// Deterministic-gate path: GetPullRequest errors before any verdict is
			// formed, so the step escalates (failed) WITHOUT writing ## Review (that
			// section is a verdict, not a pre-check) and without touching assignee.
			fakeGH.GetPullRequestReturns(nil, testError("github api down"))
			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("fetch PR"))
			_, ok := md.FindSection("## Review")
			Expect(ok).To(BeFalse())
			assertUntouched(md)
			// The read-only Claude review never ran (gate failed first).
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
		})

		It("fails when a matched spec is not completed", func() {
			// Worktree with the spec still in-progress, not completed.
			dir := GinkgoT().TempDir()
			inProgress := filepath.Join(dir, "specs", "in-progress")
			Expect(os.MkdirAll(inProgress, 0750)).To(BeNil())
			spec := "---\napproved: true\n---\n\nbody\n"
			Expect(
				os.WriteFile(filepath.Join(inProgress, "001-hello.md"), []byte(spec), 0600),
			).To(BeNil())
			fakeRepo.EnsureWorktreeReturns(dir, nil)

			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("not completed"))
			assertUntouched(md)
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
		})

		It("fails when prompts are still in-flight", func() {
			inFlight := filepath.Join(worktree, "prompts", "in-progress")
			Expect(os.MkdirAll(inFlight, 0750)).To(BeNil())
			Expect(os.WriteFile(filepath.Join(inFlight, "p1.md"), []byte("x"), 0600)).To(BeNil())

			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("in-progress"))
			assertUntouched(md)
			Expect(fakeRunner.RunCallCount()).To(Equal(0))
		})

		It("fails (writes ## Review concerns) when claude surfaces concerns", func() {
			fakeRunner.RunReturns(&claudelib.ClaudeResult{
				Result: `{"outcome":"concerns","notes":"diff is out of scope"}`,
			}, nil)
			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("blocking concerns"))
			assertUntouched(md)

			// The verdict is recorded durably so the replay re-escalates.
			out, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(out.Outcome).To(Equal("concerns"))
		})

		It("fails closed (concerns) when the claude runner errors", func() {
			fakeRunner.RunReturns(nil, testError("claude not found on PATH"))
			result, md := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			out, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(out.Outcome).To(Equal("concerns"))
		})

		It("fails closed (concerns) when the verdict is unparseable", func() {
			fakeRunner.RunReturns(&claudelib.ClaudeResult{Result: "no json here"}, nil)
			result, _ := run(reviewTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		})

		It("fails when ## Result is missing (execution did not run)", func() {
			task := `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: ai_review
repo: bborbe/sandbox
clone_url: https://github.com/bborbe/sandbox.git
ref: abc123
branch: feature/x
pr_number: 7
task_identifier: ` + validTaskID + `
---

## Plan

` + "```json" + `
{"repo":"bborbe/sandbox","matched_specs":["specs/in-progress/001-hello.md"]}
` + "```" + `
`
			result, md := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("Result"))
			assertUntouched(md)
			Expect(fakeGH.GetPullRequestCallCount()).To(Equal(0))
		})

		It("needs_input when a required frontmatter field is missing", func() {
			task := `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: ai_review
repo: bborbe/sandbox
ref: abc123
branch: feature/x
pr_number: 7
task_identifier: ` + validTaskID + `
---

## Plan

` + "```json" + `
{"repo":"bborbe/sandbox","matched_specs":["specs/in-progress/001-hello.md"]}
` + "```" + `

## Result

` + "```json" + `
{"repo":"bborbe/sandbox","specs_completed":["001-hello"]}
` + "```" + `
`
			result, md := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("clone_url"))
			assertUntouched(md)
		})
	})
})
