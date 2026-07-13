// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg/git"
)

var _ = Describe("IsValidBranchName", func() {
	DescribeTable("branch name validation",
		func(branch string, expected bool) {
			Expect(git.IsValidBranchName(branch)).To(Equal(expected))
		},
		Entry("empty string is invalid", "", false),
		Entry("starts with dash is invalid", "-bad", false),
		Entry("double-dot traversal is invalid", "branch/../../../etc/passwd", false),
		Entry("upload-pack injection is invalid", "--upload-pack=cmd", false),
		Entry("simple branch name is valid", "main", true),
		Entry("feature branch with slash is valid", "feature/my-branch", true),
		Entry("branch with underscore is valid", "my_feature", true),
		Entry("branch with dot is valid", "release-1.2", true),
		Entry("40-char commit sha is valid", "0123456789abcdef0123456789abcdef01234567", true),
	)
})

var _ = Describe("IsGitAuthFailure", func() {
	It("returns false for nil", func() {
		Expect(git.IsGitAuthFailure(nil)).To(BeFalse())
	})

	It("returns true for an authentication-failed message", func() {
		Expect(
			git.IsGitAuthFailure(testError("fatal: Authentication failed for repo")),
		).To(BeTrue())
	})

	It("returns true for a private-repo not-found message", func() {
		Expect(git.IsGitAuthFailure(testError("remote: Repository not found"))).To(BeTrue())
	})

	It("returns false for an unrelated error", func() {
		Expect(git.IsGitAuthFailure(testError("could not resolve host"))).To(BeFalse())
	})
})

type testError string

func (e testError) Error() string { return string(e) }
