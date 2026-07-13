// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
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
