// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"time"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("lifecycle timeout resolution", func() {
	Describe("resolveLifecycleTimeoutFromEnv", func() {
		It("falls back to the default when LIFECYCLE_TIMEOUT is unset", func() {
			os.Unsetenv("LIFECYCLE_TIMEOUT")
			Expect(pkg.ResolveLifecycleTimeoutFromEnv(90 * time.Minute)).To(Equal(90 * time.Minute))
		})

		It("parses a valid LIFECYCLE_TIMEOUT", func() {
			os.Setenv("LIFECYCLE_TIMEOUT", "2h")
			DeferCleanup(os.Unsetenv, "LIFECYCLE_TIMEOUT")
			Expect(pkg.ResolveLifecycleTimeoutFromEnv(90 * time.Minute)).To(Equal(2 * time.Hour))
		})

		It("falls back on an unparsable LIFECYCLE_TIMEOUT", func() {
			os.Setenv("LIFECYCLE_TIMEOUT", "not-a-duration")
			DeferCleanup(os.Unsetenv, "LIFECYCLE_TIMEOUT")
			Expect(pkg.ResolveLifecycleTimeoutFromEnv(90 * time.Minute)).To(Equal(90 * time.Minute))
		})

		It("falls back on a non-positive LIFECYCLE_TIMEOUT", func() {
			os.Setenv("LIFECYCLE_TIMEOUT", "0s")
			DeferCleanup(os.Unsetenv, "LIFECYCLE_TIMEOUT")
			Expect(pkg.ResolveLifecycleTimeoutFromEnv(90 * time.Minute)).To(Equal(90 * time.Minute))
		})
	})

	Describe("resolveLifecycleTimeout", func() {
		parse := func(content string) *agentlib.Markdown {
			md, err := agentlib.ParseMarkdown(context.Background(), content)
			Expect(err).To(BeNil())
			return md
		}

		It("returns zero when the task frontmatter has no override", func() {
			md := parse("---\nrepo: bborbe/x\n---\n\n# Body\n")
			Expect(pkg.ResolveLifecycleTimeout(md)).To(Equal(time.Duration(0)))
		})

		It("parses lifecycle_timeout_minutes from the task frontmatter", func() {
			md := parse("---\nlifecycle_timeout_minutes: 120\n---\n\n# Body\n")
			Expect(pkg.ResolveLifecycleTimeout(md)).To(Equal(2 * time.Hour))
		})

		It("returns zero for a non-positive override", func() {
			md := parse("---\nlifecycle_timeout_minutes: 0\n---\n\n# Body\n")
			Expect(pkg.ResolveLifecycleTimeout(md)).To(Equal(time.Duration(0)))
		})
	})
})
