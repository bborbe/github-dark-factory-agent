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

	BeforeEach(func() {
		ctx = context.Background()
		// currentToken prefers the live process value, so an ambient GH_TOKEN
		// (a dev shell, or the dark-factory execution phase that exports it)
		// would override the captured token these specs assert on. Each spec
		// opts in to the value it wants to observe.
		GinkgoT().Setenv("GH_TOKEN", "")
	})

	It("prefers the live GH_TOKEN over the token captured at construction", func() {
		// Installation tokens expire after 60 minutes and the execution phase
		// re-mints before its push; ai_review runs later in the same process, so
		// its REST calls must use the refreshed value, not the startup one.
		GinkgoT().Setenv("GH_TOKEN", "live")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Authorization")).To(Equal("token live"))
			_, _ = w.Write([]byte(`{"draft":true,"head":{"sha":"abc123"}}`))
		}))
		DeferCleanup(srv.Close)

		client := pkg.NewGitHubClientWithBaseURL("captured", srv.URL)
		_, err := client.GetPullRequest(ctx, "bborbe", "sandbox", 7)
		Expect(err).To(BeNil())
	})

	It("sends no Authorization header when neither a live nor a captured token exists", func() {
		// Public repos need no credential; an empty header must not become a
		// bare "token " prefix that GitHub rejects.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Authorization")).To(BeEmpty())
			_, _ = w.Write([]byte(`{"draft":true,"head":{"sha":"abc123"}}`))
		}))
		DeferCleanup(srv.Close)

		client := pkg.NewGitHubClientWithBaseURL("", srv.URL)
		_, err := client.GetPullRequest(ctx, "bborbe", "sandbox", 7)
		Expect(err).To(BeNil())
	})

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
