// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg/prompts"
)

var _ = Describe("BuildInstructions", func() {
	It("returns an empty instruction set (prompt-free in Increment 1)", func() {
		instrs := prompts.BuildInstructions()
		Expect(instrs).To(BeEmpty())
	})
})

var _ = Describe("ReviewPrompt", func() {
	It("returns a non-empty prompt mentioning the read-only contract", func() {
		p := prompts.ReviewPrompt()
		Expect(p).NotTo(BeEmpty())
		Expect(p).To(ContainSubstring("read-only"))
		Expect(p).To(ContainSubstring("outcome"))
	})
})

var _ = Describe("ParseReviewVerdict", func() {
	ctx := context.Background()

	DescribeTable("valid inputs",
		func(raw, wantOutcome string) {
			v, err := prompts.ParseReviewVerdict(ctx, raw)
			Expect(err).To(BeNil())
			Expect(v.Outcome).To(Equal(wantOutcome))
			Expect(v.Notes).NotTo(BeEmpty())
		},
		Entry("plain pass", `{"outcome":"pass","notes":"looks good"}`, "pass"),
		Entry("plain concerns", `{"outcome":"concerns","notes":"scope drift"}`, "concerns"),
		Entry("fenced json", "```json\n{\"outcome\":\"pass\",\"notes\":\"ok\"}\n```", "pass"),
		Entry(
			"json embedded in prose",
			"Here is my verdict:\n{\"outcome\":\"concerns\",\"notes\":\"missing tests\"}\nDone.",
			"concerns",
		),
	)

	DescribeTable("invalid inputs",
		func(raw string) {
			_, err := prompts.ParseReviewVerdict(ctx, raw)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("parse review verdict"))
		},
		Entry("no json", "there is no verdict here"),
		Entry("invalid outcome", `{"outcome":"maybe","notes":"unsure"}`),
		Entry("missing notes", `{"outcome":"pass","notes":"  "}`),
		Entry("malformed json block", `prefix {"outcome": "pass", }`),
	)
})
