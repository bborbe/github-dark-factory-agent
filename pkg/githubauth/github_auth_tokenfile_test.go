// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubauth_test

import (
	"context"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg/githubauth"
)

var _ = Describe("FileBackedHelper", func() {
	var (
		ctx       context.Context
		execCalls [][]string
		execFunc  func(ctx context.Context, name string, args ...string) ([]byte, error)
	)

	BeforeEach(func() {
		ctx = context.Background()
		execCalls = nil
		execFunc = func(_ context.Context, name string, args ...string) ([]byte, error) {
			execCalls = append(execCalls, append([]string{name}, args...))
			return nil, nil
		}
		Expect(os.Setenv("HOME", GinkgoT().TempDir())).To(Succeed())
	})

	It("writes the token file with 0600 and installs a file-backed helper", func() {
		Expect(githubauth.NewFileBackedHelperWithExec("tok-123", execFunc).Setup(ctx)).To(Succeed())

		path := githubauth.TokenFilePath()
		content, err := os.ReadFile(path) // #nosec G304 -- test-only path
		Expect(err).To(BeNil())
		Expect(string(content)).To(Equal("tok-123"))

		info, err := os.Stat(path)
		Expect(err).To(BeNil())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))

		Expect(execCalls).To(HaveLen(1))
		Expect(execCalls[0][0]).To(Equal("git"))
		Expect(strings.Join(execCalls[0], " ")).To(ContainSubstring("--replace-all"))
		Expect(
			strings.Join(execCalls[0], " "),
		).To(ContainSubstring("credential.https://github.com.helper"))
		Expect(strings.Join(execCalls[0], " ")).To(ContainSubstring(path))
	})

	It("is a no-op when the token is empty", func() {
		Expect(githubauth.NewFileBackedHelperWithExec("", execFunc).Setup(ctx)).To(Succeed())
		Expect(execCalls).To(BeEmpty())
		_, err := os.Stat(githubauth.TokenFilePath())
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("refuses to write an empty token file", func() {
		Expect(githubauth.WriteTokenFile("")).NotTo(Succeed())
	})

	It("replaces the file contents on a refresh", func() {
		Expect(githubauth.WriteTokenFile("first")).To(Succeed())
		Expect(githubauth.WriteTokenFile("second")).To(Succeed())
		content, err := os.ReadFile(githubauth.TokenFilePath()) // #nosec G304 -- test-only path
		Expect(err).To(BeNil())
		Expect(string(content)).To(Equal("second"))
	})
})
