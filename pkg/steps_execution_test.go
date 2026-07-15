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

// execTask builds a planning-complete task: full frontmatter plus a ## Plan
// section naming one matched spec, which is what the execution phase consumes.
func execTask() string {
	return `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: execution
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
`
}

var _ = Describe("ExecutionStep", func() {
	var (
		ctx        context.Context
		fakeRepo   *mocks.RepoManager
		fakeRunner *mocks.ExecutionRunner
		worktree   string
		step       agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		fakeRepo = &mocks.RepoManager{}
		fakeRunner = &mocks.ExecutionRunner{}
		worktree = GinkgoT().TempDir()
		fakeRepo.EnsureWorktreeReturns(worktree, nil)
		step = pkg.NewExecutionStep(
			fakeRepo,
			fakeRunner,
			claudelib.ClaudeModel("MiniMax-M2.7-highspeed"),
		)
	})

	run := func(taskContent string) (*agentlib.Result, *agentlib.Markdown) {
		md, err := agentlib.ParseMarkdown(ctx, taskContent)
		Expect(err).To(BeNil())
		result, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		return result, md
	}

	Describe("happy path (spec ends verifying)", func() {
		BeforeEach(func() {
			fakeRunner.RunLifecycleReturns(&pkg.LifecycleResult{
				PromptsExecuted: 1,
				SpecStatuses:    map[string]string{"001-hello": "verifying"},
			}, nil)
		})

		It(
			"runs dark-factory with backend:local, completes+pushes, writes ## Result, routes ai_review",
			func() {
				result, md := run(execTask())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("ai_review"))

				// dark-factory driven with the backend:local override + spec id.
				Expect(fakeRunner.RunLifecycleCallCount()).To(Equal(1))
				_, wd, specIDs, flags := fakeRunner.RunLifecycleArgsForCall(0)
				Expect(wd).To(Equal(worktree))
				Expect(specIDs).To(ConsistOf("001-hello"))
				Expect(flags).To(ContainElements("--set", "backend=local"))
				Expect(flags).To(ContainElement("--auto-approve-prompts"))
				// the injected fleet model is forwarded to the daemon so
				// backend:local never falls back to its Claude default.
				Expect(flags).To(ContainElements("--set", "model=MiniMax-M2.7-highspeed"))

				// verifying spec → agent drives `spec complete`.
				Expect(fakeRunner.CompleteSpecCallCount()).To(Equal(1))
				_, csWD, csID := fakeRunner.CompleteSpecArgsForCall(0)
				Expect(csWD).To(Equal(worktree))
				Expect(csID).To(Equal("001-hello"))

				// per-prompt commits pushed to the PR branch.
				Expect(fakeRunner.PushBranchCallCount()).To(Equal(1))
				_, pbWD, pbBranch := fakeRunner.PushBranchArgsForCall(0)
				Expect(pbWD).To(Equal(worktree))
				Expect(pbBranch).To(Equal("feature/x"))

				out, err := agentlib.ExtractSection[pkg.ExecutionOutput](ctx, md, "## Result")
				Expect(err).To(BeNil())
				Expect(out.Repo).To(Equal("bborbe/sandbox"))
				Expect(out.Branch).To(Equal("feature/x"))
				Expect(out.SpecsCompleted).To(ConsistOf("001-hello"))
				Expect(out.PromptsExecuted).To(Equal(1))
				Expect(out.CommitsPushed).To(BeTrue())
			},
		)

		It(
			"never writes .dark-factory.yaml into the branch worktree (config via --set only)",
			func() {
				run(execTask())
				_, statErr := os.Stat(filepath.Join(worktree, ".dark-factory.yaml"))
				Expect(os.IsNotExist(statErr)).To(BeTrue())
			},
		)

		It(
			"passes --set hideGit=true so dark-factory runs inside the git worktree (spec-084 gate)",
			func() {
				// The RepoManager runs the lifecycle in a worktree whose .git is a
				// file; dark-factory refuses to start there without hideGit=true.
				// Guarding the flag here makes its silent removal a test failure.
				// The actual spec-084 bypass (dark-factory starting inside the
				// worktree) can only be exercised end-to-end against a real
				// dark-factory binary — it has no in-process seam — so that half is
				// covered by the live E2E, not this unit test.
				run(execTask())
				Expect(fakeRunner.RunLifecycleCallCount()).To(Equal(1))
				_, _, _, flags := fakeRunner.RunLifecycleArgsForCall(0)
				Expect(flags).To(ContainElement("hideGit=true"))
			},
		)
	})

	Describe("model injection", func() {
		BeforeEach(func() {
			fakeRunner.RunLifecycleReturns(&pkg.LifecycleResult{
				PromptsExecuted: 1,
				SpecStatuses:    map[string]string{"001-hello": "verifying"},
			}, nil)
		})

		It(
			"omits --set model entirely when no fleet model is injected (local/test fallback)",
			func() {
				// Empty model → no `--set model=` override, so the daemon keeps its own
				// default. Guards the fallback branch of daemonFlags against a regression
				// that would smuggle an empty `model=` (or drop backend=local).
				step = pkg.NewExecutionStep(fakeRepo, fakeRunner, claudelib.ClaudeModel(""))
				run(execTask())
				Expect(fakeRunner.RunLifecycleCallCount()).To(Equal(1))
				_, _, _, flags := fakeRunner.RunLifecycleArgsForCall(0)
				Expect(flags).To(ContainElements("--set", "backend=local"))
				Expect(flags).NotTo(ContainElement(HavePrefix("model=")))
			},
		)
	})

	Describe("spec auto-completed by workflow:direct", func() {
		It("skips spec complete but still pushes and writes ## Result", func() {
			fakeRunner.RunLifecycleReturns(&pkg.LifecycleResult{
				PromptsExecuted: 2,
				SpecStatuses:    map[string]string{"001-hello": "completed"},
			}, nil)
			result, md := run(execTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
			Expect(fakeRunner.CompleteSpecCallCount()).To(Equal(0))
			Expect(fakeRunner.PushBranchCallCount()).To(Equal(1))
			out, err := agentlib.ExtractSection[pkg.ExecutionOutput](ctx, md, "## Result")
			Expect(err).To(BeNil())
			Expect(out.SpecsCompleted).To(ConsistOf("001-hello"))
		})
	})

	Describe("idempotency", func() {
		It("routes to ai_review without re-running when ## Result already exists", func() {
			result, _ := run(execTask() + "\n## Result\n\n```json\n{}\n```\n")
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
			Expect(fakeRunner.RunLifecycleCallCount()).To(Equal(0))
		})
	})

	Describe("escalation paths", func() {
		assertEscalated := func(md *agentlib.Markdown) {
			// Doctrine: assignee + status untouched, no ## Result written.
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-dark-factory-agent"))
			status, _ := md.Frontmatter.String("status")
			Expect(status).To(Equal("in_progress"))
			_, ok := md.FindSection("## Result")
			Expect(ok).To(BeFalse())
		}

		It(
			"fails (Status failed) when the dark-factory lifecycle errors — assignee untouched",
			func() {
				fakeRunner.RunLifecycleReturns(nil, testError("claude not found on PATH"))
				result, md := run(execTask())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("lifecycle failed"))
				assertEscalated(md)
				// No auto-fix loop: exactly one attempt, no spec-complete / push.
				Expect(fakeRunner.RunLifecycleCallCount()).To(Equal(1))
				Expect(fakeRunner.CompleteSpecCallCount()).To(Equal(0))
				Expect(fakeRunner.PushBranchCallCount()).To(Equal(0))
			},
		)

		It(
			"fails when a spec never reached verification (prompt failed DoD/audit, fail-closed)",
			func() {
				fakeRunner.RunLifecycleReturns(&pkg.LifecycleResult{
					PromptsExecuted: 0,
					SpecStatuses:    map[string]string{"001-hello": "prompted"},
				}, nil)
				result, md := run(execTask())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("did not reach verification"))
				assertEscalated(md)
				Expect(fakeRunner.PushBranchCallCount()).To(Equal(0))
			},
		)

		It("fails when spec complete errors", func() {
			fakeRunner.RunLifecycleReturns(&pkg.LifecycleResult{
				PromptsExecuted: 1,
				SpecStatuses:    map[string]string{"001-hello": "verifying"},
			}, nil)
			fakeRunner.CompleteSpecReturns(testError("transition rejected"))
			result, md := run(execTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("spec complete"))
			assertEscalated(md)
			Expect(fakeRunner.PushBranchCallCount()).To(Equal(0))
		})

		It("fails when the push errors", func() {
			fakeRunner.RunLifecycleReturns(&pkg.LifecycleResult{
				PromptsExecuted: 1,
				SpecStatuses:    map[string]string{"001-hello": "verifying"},
			}, nil)
			fakeRunner.PushBranchReturns(testError("remote rejected"))
			result, md := run(execTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("push branch"))
			assertEscalated(md)
		})

		It("fails when ## Plan is missing", func() {
			task := `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: execution
clone_url: https://github.com/bborbe/sandbox.git
ref: abc123
branch: feature/x
task_identifier: ` + validTaskID + `
---

body
`
			result, md := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("Plan"))
			assertEscalated(md)
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
		})

		It("needs_input when a required frontmatter field is missing", func() {
			task := `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: execution
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
`
			result, md := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("clone_url"))
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-dark-factory-agent"))
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
		})
	})
})
