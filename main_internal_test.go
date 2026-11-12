// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"testing"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/argument/v2"
	berrors "github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	sentry "github.com/getsentry/sentry-go"
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
		message string
		wantErr bool
	}{
		{
			name:    "failed must fail the Job so the executor does not record a phase success",
			status:  agentlib.AgentStatusFailed,
			message: "execution: ensure worktree: clone boom",
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
			err := exitErrorForStatus(ctx, c.status, phase, c.message)
			if c.wantErr && err == nil {
				t.Fatalf(
					"status %q: expected a non-nil error, got nil — the Job would exit 0 and the executor would record it as a phase success",
					c.status,
				)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("status %q: expected nil, got %v", c.status, err)
			}
			if c.wantErr && !strings.Contains(err.Error(), c.message) {
				t.Fatalf(
					"failed error must include Result.Message %q, got %q",
					c.message,
					err.Error(),
				)
			}
		})
	}
}

func TestExitErrorForStatusTruncatesLongMessage(t *testing.T) {
	ctx := context.Background()
	message := strings.Repeat("a", statusMessageMaxRunes+100)

	err := exitErrorForStatus(
		ctx,
		agentlib.AgentStatusFailed,
		domain.TaskPhase("ai_review"),
		message,
	)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), strings.Repeat("a", statusMessageMaxRunes)+"...") {
		t.Fatalf(
			"want the message truncated at %d runes with an ellipsis, got %q",
			statusMessageMaxRunes,
			err.Error(),
		)
	}
	if strings.Contains(err.Error(), strings.Repeat("a", statusMessageMaxRunes+1)) {
		t.Fatalf("message was not truncated: %q", err.Error())
	}
}

func TestFinishRunAttachesSentryTags(t *testing.T) {
	ctx := context.Background()
	result := &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: "planning: PR #7 is not a draft",
	}

	err := finishRun(
		ctx,
		result,
		domain.TaskPhasePlanning,
		agentlib.TaskIdentifier("task-123"),
		"llm",
	)
	if err == nil {
		t.Fatal("expected a non-nil error for a failed result")
	}

	data := berrors.DataFromError(err)
	want := map[string]string{
		"task_id":   "task-123",
		"task_type": "llm",
		"phase":     "planning",
		"message":   "planning: PR #7 is not a draft",
	}
	for key, wantValue := range want {
		if got := fmt.Sprintf("%v", data[key]); got != wantValue {
			t.Fatalf("Sentry tag %q = %q, want %q", key, got, wantValue)
		}
	}
}

func TestFinishRunTruncatesMessageTag(t *testing.T) {
	ctx := context.Background()
	message := strings.Repeat("a", statusMessageMaxRunes+100)
	result := &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: message,
	}

	err := finishRun(
		ctx,
		result,
		domain.TaskPhaseAIReview,
		agentlib.TaskIdentifier("task-456"),
		"llm",
	)
	if err == nil {
		t.Fatal("expected a non-nil error for a failed result")
	}

	got := fmt.Sprintf("%v", berrors.DataFromError(err)["message"])
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("message tag must be truncated with an ellipsis, got %q", got)
	}
	if strings.Contains(got, strings.Repeat("a", statusMessageMaxRunes+1)) {
		t.Fatalf("message tag was not truncated: %q", got)
	}
}

func TestStatusErrorFingerprint(t *testing.T) {
	ctx := context.Background()

	err := exitErrorForStatus(
		ctx,
		agentlib.AgentStatusFailed,
		domain.TaskPhase("execution"),
		"execution: ensure worktree: clone boom",
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	var f fingerprinter
	if !errors.As(err, &f) {
		t.Fatalf("expected a fingerprinter error, got %T", err)
	}
	want := []string{
		"agent-status-failed",
		"execution",
		"execution: ensure worktree: clone boom",
	}
	if !slices.Equal(f.Fingerprint(), want) {
		t.Fatalf("fingerprint = %v, want %v", f.Fingerprint(), want)
	}
}

func TestSentryFingerprintProcessor(t *testing.T) {
	ctx := context.Background()

	t.Run("sets the fingerprint from a status error", func(t *testing.T) {
		err := exitErrorForStatus(
			ctx,
			agentlib.AgentStatusFailed,
			domain.TaskPhase("planning"),
			"planning: PR #7 is not a draft",
		)
		event := &sentry.Event{}
		got := sentryFingerprintProcessor(
			event,
			&sentry.EventHint{OriginalException: err},
		)
		want := []string{
			"agent-status-failed",
			"planning",
			"planning: PR #7 is not a draft",
		}
		if !slices.Equal(got.Fingerprint, want) {
			t.Fatalf("fingerprint = %v, want %v", got.Fingerprint, want)
		}
	})

	t.Run("leaves an unrelated error without a fingerprint", func(t *testing.T) {
		event := &sentry.Event{}
		got := sentryFingerprintProcessor(
			event,
			&sentry.EventHint{OriginalException: errors.New("boom")},
		)
		if len(got.Fingerprint) != 0 {
			t.Fatalf("unexpected fingerprint %v", got.Fingerprint)
		}
	})

	t.Run("nil event and hint are safe", func(t *testing.T) {
		if got := sentryFingerprintProcessor(nil, nil); got != nil {
			t.Fatalf("expected nil event, got %v", got)
		}
	})
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
