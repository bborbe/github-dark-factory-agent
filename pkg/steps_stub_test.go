// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("stub steps", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("ai_review ShouldRun returns true (routing must never be skipped)", func() {
		step := pkg.NewAIReviewStep(nil, nil, nil)
		shouldRun, err := step.ShouldRun(ctx, nil)
		Expect(err).To(BeNil())
		Expect(shouldRun).To(BeTrue())
	})

	It("step names are stable lower-kebab identifiers", func() {
		Expect(pkg.NewExecutionStep(nil, nil).Name()).To(Equal("df-execution"))
		Expect(pkg.NewAIReviewStep(nil, nil, nil).Name()).To(Equal("df-ai-review"))
		Expect(pkg.NewPlanningStep(nil, nil).Name()).To(Equal("df-planning"))
		Expect(pkg.NewClaudeAuthStep(nil).Name()).To(Equal("verify-claude-auth"))
		Expect(pkg.NewGHTokenCheckStep("").Name()).To(Equal("verify-gh-token"))
	})
})
