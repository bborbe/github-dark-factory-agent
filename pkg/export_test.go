// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"time"

	agentlib "github.com/bborbe/agent"
)

// NewExecClaudeProber exposes the exec-backed prober with an injectable
// command so tests can drive the success / unauth / error branches without a
// real claude binary.
func NewExecClaudeProber(name string, args ...string) ClaudeProber {
	return &execClaudeProber{name: name, args: args, timeout: 30 * time.Second}
}

// NewGHTokenCheckStepWithURL exposes the concrete gh-token step constructor so
// tests can point the probe at an httptest server.
func NewGHTokenCheckStepWithURL(token, httpURL string) agentlib.Step {
	return newGHTokenCheckStep(token, httpURL)
}

// NewGitHubClientWithBaseURL exposes the concrete GitHub client constructor so
// tests can point it at an httptest server.
func NewGitHubClientWithBaseURL(token, baseURL string) GitHubClient {
	return newGitHubClient(token, baseURL)
}
