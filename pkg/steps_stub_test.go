// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("stub steps", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("ai_review step fails with the Increment 3 marker", func() {
		step := pkg.NewAIReviewStep()
		shouldRun, err := step.ShouldRun(ctx, nil)
		Expect(err).To(BeNil())
		Expect(shouldRun).To(BeTrue())
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(result.Message).To(ContainSubstring("Increment 3"))
	})

	It("step names are stable lower-kebab identifiers", func() {
		Expect(pkg.NewExecutionStep(nil, nil).Name()).To(Equal("df-execution"))
		Expect(pkg.NewAIReviewStep().Name()).To(Equal("df-ai-review"))
		Expect(pkg.NewPlanningStep(nil, nil).Name()).To(Equal("df-planning"))
		Expect(pkg.NewClaudeAuthStep(nil).Name()).To(Equal("verify-claude-auth"))
		Expect(pkg.NewGHTokenCheckStep("").Name()).To(Equal("verify-gh-token"))
	})
})
