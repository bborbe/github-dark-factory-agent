// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
	"github.com/golang/glog"
)

// claudeAuthStep is a preflight that runs a trivial claude probe and fails the
// task early with a clear escalation if claude is not authenticated.
//
// Live-smoke finding (design.md § Part 5): claude auth is HOME-sensitive.
// backend: local runs claude as a subprocess inheriting the pod HOME; if the
// login credential is not discoverable there, every prompt fails "Not logged
// in". Probing once up front turns N per-prompt failures into one actionable
// escalation.
type claudeAuthStep struct {
	prober ClaudeProber
}

// NewClaudeAuthStep wires the claude-auth preflight with its prober seam.
func NewClaudeAuthStep(prober ClaudeProber) agentlib.Step {
	return &claudeAuthStep{prober: prober}
}

// Name implements agentlib.Step.
func (s *claudeAuthStep) Name() string { return "verify-claude-auth" }

// ShouldRun always returns true — auth can lapse between phases.
func (s *claudeAuthStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run probes claude and returns Done+ContinueToNext when authenticated, or
// Failed with a clear escalation message otherwise.
func (s *claudeAuthStep) Run(ctx context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	if err := s.prober.Probe(ctx); err != nil {
		glog.V(2).Infof("claude-auth: probe failed: %v", err)
		return failed(
			"claude not authenticated (" + err.Error() + ") — pod HOME must contain claude login; " +
				"backend:local inherits it (bake/mount the credential into the runtime HOME, " +
				"CLAUDE_CONFIG_DIR alone is not enough)",
		), nil
	}
	return &agentlib.Result{
		Status:         agentlib.AgentStatusDone,
		ContinueToNext: true,
	}, nil
}
