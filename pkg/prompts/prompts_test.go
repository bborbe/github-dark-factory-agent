// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/agent-claude/pkg/prompts"
)

var _ = Describe("BuildInstructions", func() {
	It("returns exactly 2 instructions", func() {
		instrs := prompts.BuildInstructions()
		Expect(instrs).To(HaveLen(2))
	})

	It("first instruction is workflow", func() {
		instrs := prompts.BuildInstructions()
		Expect(instrs[0].Name).To(Equal("workflow"))
		Expect(instrs[0].Content).NotTo(BeEmpty())
	})

	It("second instruction is output-format", func() {
		instrs := prompts.BuildInstructions()
		Expect(instrs[1].Name).To(Equal("output-format"))
		Expect(instrs[1].Content).NotTo(BeEmpty())
	})
})
