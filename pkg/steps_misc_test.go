// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/mocks"
	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("step ShouldRun guards", func() {
	ctx := context.Background()

	It("planning ShouldRun returns true (routing must never be skipped)", func() {
		ok, err := pkg.NewPlanningStep(nil, nil).ShouldRun(ctx, nil)
		Expect(err).To(BeNil())
		Expect(ok).To(BeTrue())
	})

	It("gh-token ShouldRun returns true", func() {
		ok, err := pkg.NewGHTokenCheckStep("").ShouldRun(ctx, nil)
		Expect(err).To(BeNil())
		Expect(ok).To(BeTrue())
	})
})

var _ = Describe("planning malformed frontmatter", func() {
	var (
		ctx  context.Context
		step agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		step = pkg.NewPlanningStep(&mocks.RepoManager{}, &mocks.GitHubClient{})
	})

	run := func(task string) *agentlib.Result {
		md, err := agentlib.ParseMarkdown(ctx, task)
		Expect(err).To(BeNil())
		result, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		return result
	}

	It("needs_input when pr_number is non-numeric", func() {
		task := `---
clone_url: https://github.com/bborbe/sandbox.git
ref: abc123
branch: feature/x
pr_number: "notanumber"
task_identifier: bd4d883b-1234-5678-abcd-123456789012
repo: bborbe/sandbox
---
body
`
		result := run(task)
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(result.Message).To(ContainSubstring("pr_number"))
	})

	It("needs_input when repo is not owner/name", func() {
		task := `---
clone_url: https://github.com/bborbe/sandbox.git
ref: abc123
branch: feature/x
pr_number: 7
task_identifier: bd4d883b-1234-5678-abcd-123456789012
repo: noslash
---
body
`
		result := run(task)
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(result.Message).To(ContainSubstring("owner/name"))
	})
})

var _ = Describe("constructors", func() {
	It("NewGitHubClient returns a non-nil client", func() {
		Expect(pkg.NewGitHubClient("")).NotTo(BeNil())
	})

	It("NewClaudeProber returns a non-nil prober", func() {
		Expect(pkg.NewClaudeProber(claudelib.ClaudeConfigDir(""))).NotTo(BeNil())
	})
})
