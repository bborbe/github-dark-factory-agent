// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/mocks"
	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("ClaudeAuthStep", func() {
	var (
		ctx    context.Context
		prober *mocks.ClaudeProber
		step   agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		prober = &mocks.ClaudeProber{}
		step = pkg.NewClaudeAuthStep(prober)
	})

	It("done + continue when claude is authenticated", func() {
		prober.ProbeReturns(nil)
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.ContinueToNext).To(BeTrue())
	})

	It("failed with a HOME-sensitive escalation when unauthenticated", func() {
		prober.ProbeReturns(testError("Not logged in"))
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(result.Message).To(ContainSubstring("Not logged in"))
		Expect(result.Message).To(ContainSubstring("pod HOME"))
	})

	It("ShouldRun always returns true", func() {
		shouldRun, err := step.ShouldRun(ctx, nil)
		Expect(err).To(BeNil())
		Expect(shouldRun).To(BeTrue())
	})
})

var _ = Describe("exec ClaudeProber", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("returns nil when the probe command exits cleanly", func() {
		Expect(pkg.NewExecClaudeProber("true").Probe(ctx)).To(BeNil())
	})

	It("returns an error on a non-zero exit", func() {
		err := pkg.NewExecClaudeProber("false").Probe(ctx)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("claude probe failed"))
	})

	It("returns an error when output carries an unauth marker", func() {
		err := pkg.NewExecClaudeProber("printf", "Not logged in").Probe(ctx)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("not authenticated"))
	})
})
