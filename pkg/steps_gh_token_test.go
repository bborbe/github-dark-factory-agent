// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg"
)

var _ = Describe("GHTokenCheckStep", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	serve := func(status int, body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		DeferCleanup(srv.Close)
		return srv
	}

	It("needs_input when the token is empty", func() {
		step := pkg.NewGHTokenCheckStepWithURL("", "http://unused")
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
	})

	It("done + continue on a healthy token", func() {
		srv := serve(http.StatusOK, `{"resources":{"core":{"limit":5000,"remaining":5000}}}`)
		step := pkg.NewGHTokenCheckStepWithURL("tok", srv.URL)
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.ContinueToNext).To(BeTrue())
	})

	It("needs_input on HTTP 401", func() {
		srv := serve(http.StatusUnauthorized, `{}`)
		step := pkg.NewGHTokenCheckStepWithURL("tok", srv.URL)
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
	})

	It("needs_input when the token degrades to anonymous", func() {
		srv := serve(http.StatusOK, `{"resources":{"core":{"limit":60,"remaining":60}}}`)
		step := pkg.NewGHTokenCheckStepWithURL("tok", srv.URL)
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
	})

	It("failed when the quota is nearly exhausted", func() {
		srv := serve(http.StatusOK, `{"resources":{"core":{"limit":5000,"remaining":5}}}`)
		step := pkg.NewGHTokenCheckStepWithURL("tok", srv.URL)
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
	})

	It("failed on a non-200 rate_limit response", func() {
		srv := serve(http.StatusInternalServerError, `boom`)
		step := pkg.NewGHTokenCheckStepWithURL("tok", srv.URL)
		result, err := step.Run(ctx, nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
	})
})
