// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("GitHubClient", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("GetPullRequest parses draft flag and head sha", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/repos/bborbe/sandbox/pulls/7"))
			Expect(r.Header.Get("Authorization")).To(Equal("token tok"))
			_, _ = w.Write([]byte(`{"draft":true,"head":{"sha":"abc123"}}`))
		}))
		DeferCleanup(srv.Close)

		client := pkg.NewGitHubClientWithBaseURL("tok", srv.URL)
		info, err := client.GetPullRequest(ctx, "bborbe", "sandbox", 7)
		Expect(err).To(BeNil())
		Expect(info.Draft).To(BeTrue())
		Expect(info.HeadSHA).To(Equal("abc123"))
	})

	It("ListPullRequestFiles parses filenames", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(
				[]byte(`[{"filename":"specs/in-progress/foo.md"},{"filename":"README.md"}]`),
			)
		}))
		DeferCleanup(srv.Close)

		client := pkg.NewGitHubClientWithBaseURL("tok", srv.URL)
		files, err := client.ListPullRequestFiles(ctx, "bborbe", "sandbox", 7)
		Expect(err).To(BeNil())
		Expect(files).To(ConsistOf("specs/in-progress/foo.md", "README.md"))
	})

	It("returns an error on a non-200 response", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}))
		DeferCleanup(srv.Close)

		client := pkg.NewGitHubClientWithBaseURL("tok", srv.URL)
		_, err := client.GetPullRequest(ctx, "bborbe", "sandbox", 7)
		Expect(err).NotTo(BeNil())
	})
})
