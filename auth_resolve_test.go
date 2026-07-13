// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the unexported resolveAuth. They run under
// the Ginkgo suite bootstrapped in main_test.go.
//
// The neither-set and fallback cases are hermetic: useGitHubApp is false, so
// githubapp.MintIAT is never reached. The App-mode branch is exercised with an
// invalid inline PEM, which makes MintIAT fail while parsing the key —
// no HTTP is performed — proving the App branch was selected and MintIAT was
// called without depending on a live GitHub mint. The App-mode *success* path
// is covered by lib/githubapp's own httptest-backed MintIAT tests.
var _ = Describe("application.resolveAuth", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("errors when neither App credentials nor GH_TOKEN are configured", func() {
		app := &application{}
		token, err := app.resolveAuth(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no credentials configured"))
		Expect(token).To(BeEmpty())
	})

	It("falls back to the raw GH_TOKEN when no App credentials are set", func() {
		app := &application{GhToken: "ghp_local_fallback_token"}
		token, err := app.resolveAuth(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(Equal("ghp_local_fallback_token"))
	})

	It(
		"treats App ID + Installation ID without a PEM as not-App and falls back / errors",
		func() {
			// useGitHubApp requires a PEM; without one the App branch is skipped.
			// With no GhToken either, resolveAuth returns the neither-set error.
			app := &application{AppID: 1, InstallationID: 2}
			token, err := app.resolveAuth(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no credentials configured"))
			Expect(token).To(BeEmpty())
		},
	)

	It("selects the App branch and calls MintIAT when App creds + a PEM are set", func() {
		// Invalid inline PEM → MintIAT fails parsing the key (no network),
		// proving the App branch ran and MintIAT was invoked.
		app := &application{
			AppID:          1,
			InstallationID: 2,
			PEMKey:         "-----BEGIN RSA PRIVATE KEY-----\nnot-a-real-key\n-----END RSA PRIVATE KEY-----",
		}
		token, err := app.resolveAuth(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("mint github app iat"))
		Expect(token).To(BeEmpty())
	})

	It("prefers App auth over the raw GH_TOKEN when both are present", func() {
		// Both App creds (with an invalid PEM) and GhToken set: the App branch
		// wins, so the result is the mint error — NOT a silent GhToken fallback.
		app := &application{
			AppID:          1,
			InstallationID: 2,
			PEMKey:         "-----BEGIN RSA PRIVATE KEY-----\nnot-a-real-key\n-----END RSA PRIVATE KEY-----",
			GhToken:        "ghp_should_not_be_used",
		}
		token, err := app.resolveAuth(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("mint github app iat"))
		Expect(token).To(BeEmpty())
	})
})
