// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"time"

	agentlib "github.com/bborbe/agent"
)

// NewTestExecutionRunner exposes the production runner with an injectable
// binary + fast poll/timeout so tests can point it at a stub `dark-factory`
// script and drive the real daemon/spec-complete/push flow without claude.
func NewTestExecutionRunner(binary string, poll, timeout time.Duration) ExecutionRunner {
	return &darkFactoryRunner{binary: binary, pollInterval: poll, timeout: timeout}
}

// ReadSpecStatus exposes the spec-status frontmatter reader for tests.
func ReadSpecStatus(ctx context.Context, workdir, id string) string {
	return readSpecStatus(ctx, workdir, id)
}

// HasInProgressPrompts exposes the queue-empty check for tests.
func HasInProgressPrompts(workdir string) bool { return hasInProgressPrompts(workdir) }

// HasInboxPrompts exposes the generation-inbox check for tests.
func HasInboxPrompts(workdir string) bool { return hasInboxPrompts(workdir) }

// CountCompletedPrompts exposes the completed-prompt counter for tests.
func CountCompletedPrompts(workdir string) int { return countCompletedPrompts(workdir) }

// FindFailedPrompt exposes the terminal-failure detector for tests.
func FindFailedPrompt(ctx context.Context, workdir string) (string, string) {
	return findFailedPrompt(ctx, workdir)
}

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
