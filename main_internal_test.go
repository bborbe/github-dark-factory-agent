// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"testing"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/vault-cli/pkg/domain"
)

// TestExitErrorForStatus guards the exit-code contract between this agent and
// the executor that watches its Job.
//
// The executor records a phase success on exit 0 alone (IsJobSucceeded in
// agent-task-executor's job_watcher) and never reads the Status this agent
// publishes. So a run that reports failure but exits 0 is written as a phase
// success, and once the success grace expires the task is parked at
// human_review as though a human had parked it deliberately.
//
// Observed 2026-11-03: task ceb79961 published Status=failed for
// "git clone --bare: Failed to connect to github.com port 443", exited 0, and
// parked at human_review.
func TestExitErrorForStatus(t *testing.T) {
	ctx := context.Background()
	phase := domain.TaskPhase("execution")

	cases := []struct {
		name    string
		status  agentlib.AgentStatus
		wantErr bool
	}{
		{
			name:    "failed must fail the Job so the executor does not record a phase success",
			status:  agentlib.AgentStatusFailed,
			wantErr: true,
		},
		{
			name:    "done exits 0",
			status:  agentlib.AgentStatusDone,
			wantErr: false,
		},
		{
			name:    "in_progress exits 0",
			status:  agentlib.AgentStatusInProgress,
			wantErr: false,
		},
		{
			// needs_input reaches the same park path on purpose: the task is meant
			// to wait at human_review for an answer, not to be re-driven.
			name:    "needs_input exits 0 — parking for a human answer is its intent",
			status:  agentlib.AgentStatusNeedsInput,
			wantErr: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := exitErrorForStatus(ctx, c.status, phase)
			if c.wantErr && err == nil {
				t.Fatalf(
					"status %q: expected a non-nil error, got nil — the Job would exit 0 and the executor would record it as a phase success",
					c.status,
				)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("status %q: expected nil, got %v", c.status, err)
			}
		})
	}
}
