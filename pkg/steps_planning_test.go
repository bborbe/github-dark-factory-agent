// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/mocks"
	"github.com/bborbe/github-dark-factory-agent/pkg"
)

const validTaskID = "bd4d883b-1234-5678-abcd-123456789012"

func baseTask() string {
	return `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: planning
repo: bborbe/sandbox
clone_url: https://github.com/bborbe/sandbox.git
ref: abc123
branch: feature/x
pr_number: 7
task_identifier: ` + validTaskID + `
---

# Dark-Factory Implement

body
`
}

// approvedWorktree builds a worktree dir with .dark-factory.yaml and one
// approved-not-completed spec under specs/in-progress/.
func approvedWorktree() string {
	dir := GinkgoT().TempDir()
	Expect(
		os.WriteFile(filepath.Join(dir, ".dark-factory.yaml"), []byte("workflow: direct\n"), 0600),
	).To(BeNil())
	specDir := filepath.Join(dir, "specs", "in-progress")
	Expect(os.MkdirAll(specDir, 0750)).To(BeNil())
	spec := "---\nkind: feature\napproved: true\n---\n\nSpec body.\n"
	Expect(os.WriteFile(filepath.Join(specDir, "foo.md"), []byte(spec), 0600)).To(BeNil())
	return dir
}

var _ = Describe("PlanningStep", func() {
	var (
		ctx      context.Context
		fakeRepo *mocks.RepoManager
		fakeGH   *mocks.GitHubClient
		step     agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		fakeRepo = &mocks.RepoManager{}
		fakeGH = &mocks.GitHubClient{}
		step = pkg.NewPlanningStep(fakeRepo, fakeGH)
	})

	run := func(taskContent string) (*agentlib.Result, *agentlib.Markdown) {
		md, err := agentlib.ParseMarkdown(ctx, taskContent)
		Expect(err).To(BeNil())
		result, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		return result, md
	}

	Describe("happy path", func() {
		BeforeEach(func() {
			fakeRepo.EnsureWorktreeReturns(approvedWorktree(), nil)
			fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: true, HeadSHA: "abc123"}, nil)
			fakeGH.ListPullRequestFilesReturns([]string{"specs/in-progress/foo.md"}, nil)
		})

		It("writes ## Plan and routes to execution", func() {
			result, md := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))

			plan, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(plan.Repo).To(Equal("bborbe/sandbox"))
			Expect(plan.PRNumber).To(Equal(7))
			Expect(plan.HeadSHA).To(Equal("abc123"))
			Expect(plan.MatchedSpecs).To(ConsistOf("specs/in-progress/foo.md"))
			Expect(plan.ChecksPassed).To(ContainElement("approved_spec_in_diff"))
		})
	})

	Describe("idempotency", func() {
		It("routes to execution without cloning when ## Plan already exists", func() {
			result, _ := run(baseTask() + "\n## Plan\n\n```json\n{}\n```\n")
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
		})
	})

	Describe("escalation paths", func() {
		BeforeEach(func() {
			fakeRepo.EnsureWorktreeReturns(approvedWorktree(), nil)
			fakeGH.ListPullRequestFilesReturns([]string{"specs/in-progress/foo.md"}, nil)
		})

		assertEscalated := func(md *agentlib.Markdown) {
			// Doctrine: assignee + status untouched, no ## Plan written.
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-dark-factory-agent"))
			status, _ := md.Frontmatter.String("status")
			Expect(status).To(Equal("in_progress"))
			_, ok := md.FindSection("## Plan")
			Expect(ok).To(BeFalse())
		}

		It("fails when ref != PR head sha (assignee untouched)", func() {
			fakeGH.GetPullRequestReturns(
				&pkg.PullRequestInfo{Draft: true, HeadSHA: "different"},
				nil,
			)
			result, md := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("head sha"))
			assertEscalated(md)
		})

		It("fails when the PR is not a draft (assignee untouched)", func() {
			fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: false, HeadSHA: "abc123"}, nil)
			result, md := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("not a draft"))
			assertEscalated(md)
		})

		It("fails when no approved-not-completed spec is in the PR diff", func() {
			fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: true, HeadSHA: "abc123"}, nil)
			fakeGH.ListPullRequestFilesReturns([]string{"README.md"}, nil) // spec not in diff
			result, md := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("no approved-not-completed spec"))
			assertEscalated(md)
		})

		It("fails when .dark-factory.yaml is missing", func() {
			bare := GinkgoT().TempDir() // no .dark-factory.yaml
			fakeRepo.EnsureWorktreeReturns(bare, nil)
			fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: true, HeadSHA: "abc123"}, nil)
			result, md := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring(".dark-factory.yaml"))
			assertEscalated(md)
		})

		It("fails when the worktree cannot be created", func() {
			fakeRepo.EnsureWorktreeReturns("", testError("clone boom"))
			result, md := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("ensure worktree"))
			assertEscalated(md)
		})
	})

	Describe("missing frontmatter", func() {
		It("returns needs_input when clone_url is absent", func() {
			task := `---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: planning
repo: bborbe/sandbox
ref: abc123
branch: feature/x
pr_number: 7
task_identifier: ` + validTaskID + `
---

body
`
			result, md := run(task)
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("clone_url"))
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-dark-factory-agent"))
			Expect(fakeRepo.EnsureWorktreeCallCount()).To(Equal(0))
		})
	})

	Describe("spec that is approved AND completed", func() {
		It("does not count as a match", func() {
			dir := GinkgoT().TempDir()
			Expect(
				os.WriteFile(
					filepath.Join(dir, ".dark-factory.yaml"),
					[]byte("workflow: direct\n"),
					0600,
				),
			).To(BeNil())
			specDir := filepath.Join(dir, "specs", "in-progress")
			Expect(os.MkdirAll(specDir, 0750)).To(BeNil())
			// Use today's date so the fixture never silently goes stale.
			completedDate := time.Now().Format("2006-01-02")
			spec := "---\napproved: true\ncompleted: " + completedDate + "\n---\n\nDone.\n"
			Expect(os.WriteFile(filepath.Join(specDir, "foo.md"), []byte(spec), 0600)).To(BeNil())

			fakeRepo.EnsureWorktreeReturns(dir, nil)
			fakeGH.GetPullRequestReturns(&pkg.PullRequestInfo{Draft: true, HeadSHA: "abc123"}, nil)
			fakeGH.ListPullRequestFilesReturns([]string{"specs/in-progress/foo.md"}, nil)

			result, _ := run(baseTask())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("no approved-not-completed spec"))
		})
	})
})

type testError string

func (e testError) Error() string { return string(e) }
