// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/argument/v2"
	"github.com/bborbe/vault-cli/pkg/domain"
)

// TestWorkdirPathsCarryNoCompiledInDefault guards the defect this change fixes:
// the pod binary used to resolve REPOS_PATH and WORK_PATH to /repos and /work
// via `default:` struct tags. The container runs as the image's unprivileged
// `node` user against a root-owned `/`, so those paths are unwritable and every
// `git worktree add` died with EACCES minutes into the run, naming no cause.
//
// It drives argument.DefaultValues — the same machinery the real parser merges
// into the struct — so re-adding either tag turns this red.
func TestWorkdirPathsCarryNoCompiledInDefault(t *testing.T) {
	defaults, err := argument.DefaultValues(context.Background(), &application{})
	if err != nil {
		t.Fatalf("collect defaults: %v", err)
	}
	for _, field := range []string{"ReposPath", "WorkPath"} {
		if value, ok := defaults[field]; ok {
			t.Fatalf(
				"%s still carries a compiled-in default %q — an unset variable would resolve to that path instead of failing at startup",
				field,
				value,
			)
		}
	}
}

// TestValidateRequiredNamesWorkdirPaths is the behavioural half of the pair
// above: with either variable unset, the startup validation the real binary
// runs must fail and name it. Today (before the fix) both fields are optional
// and this validation returns nil, so the container starts and fails later at
// `git worktree add` instead.
//
// TaskContent is set because it is the first required field the validator
// reaches; leaving it empty would mask the workdir assertions.
func TestValidateRequiredNamesWorkdirPaths(t *testing.T) {
	ctx := context.Background()

	err := argument.ValidateRequired(ctx, &application{TaskContent: "# task"})
	if err == nil || !strings.Contains(err.Error(), "REPOS_PATH") {
		t.Fatalf("unset ReposPath: want an error naming REPOS_PATH, got %v", err)
	}

	err = argument.ValidateRequired(ctx, &application{
		TaskContent: "# task",
		ReposPath:   "/repos",
	})
	if err == nil || !strings.Contains(err.Error(), "WORK_PATH") {
		t.Fatalf("unset WorkPath: want an error naming WORK_PATH, got %v", err)
	}

	err = argument.ValidateRequired(ctx, &application{
		TaskContent: "# task",
		ReposPath:   "/repos",
		WorkPath:    "/work",
	})
	if err != nil {
		t.Fatalf("both set: want nil, got %v", err)
	}
}

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

// TestPrintRedactsAnthropicAuthToken guards the startup argument dump against
// republishing the ANTHROPIC_AUTH_TOKEN value into the Job log.
//
// argument.Print honours only display:"hidden" and display:"length"; every other
// tag value falls through to the plaintext branch (`Argument: %s '%v'`). The root
// binary's application struct carried display:"password" — a value the printer does
// not implement — so each Job start wrote the live model-router credential in
// cleartext. This test drives the real printer over the real struct, so removing
// the display:"length" tag turns it red.
//
// Plain testing rather than Ginkgo on purpose: application is unexported, so the
// assertion must live in package main, and package main_test (main_test.go) shares
// this test binary — where a second RunSpecs fails with "calling RunSpecs more than
// once". An internal spec therefore cannot join the existing suite. Same shape as
// TestExitErrorForStatus above.
func TestPrintRedactsAnthropicAuthToken(t *testing.T) {
	const sentinel = "sentinel-token-do-not-log-9f3a2b7c1d"

	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	if err := argument.Print(context.Background(), &application{AnthropicAuthToken: sentinel}); err != nil {
		t.Fatalf("print: %v", err)
	}

	rendered := buf.String()
	if got := strings.Count(rendered, sentinel); got != 0 {
		t.Fatalf(
			"token value leaked into the argument dump: sentinel appeared %d times\n%s",
			got,
			rendered,
		)
	}
	if got := strings.Count(rendered, "AnthropicAuthToken"); got != 1 {
		t.Fatalf("want the field named exactly once, got %d\n%s", got, rendered)
	}
	if !strings.Contains(rendered, "Argument: AnthropicAuthToken length ") {
		t.Fatalf("want the length form, got:\n%s", rendered)
	}
}
