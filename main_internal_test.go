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
	"time"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/argument/v2"
	"github.com/bborbe/errors"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/vault-cli/pkg/domain"
	sentrygo "github.com/getsentry/sentry-go"
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
			message: "git clone --bare: connection refused",
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
			if !c.wantErr {
				if err != nil {
					t.Fatalf("status %q: expected nil, got %v", c.status, err)
				}
				return
			}
			assertFailedExitError(t, err, phase, c.status, c.message)
		})
	}
}

// assertFailedExitError guards the failure contract: the Job fails (non-nil
// error), the error names the phase and carries the phase's message, and the
// phase/status error data is present for Sentry.
func assertFailedExitError(
	t *testing.T,
	err error,
	phase domain.TaskPhase,
	status agentlib.AgentStatus,
	message string,
) {
	t.Helper()
	if err == nil {
		t.Fatalf(
			"status %q: expected a non-nil error, got nil — the Job would exit 0 and the executor would record it as a phase success",
			status,
		)
		return
	}
	if !strings.Contains(err.Error(), message) {
		t.Fatalf("expected the phase message %q in the error, got %q", message, err.Error())
	}
	if !strings.Contains(err.Error(), string(phase)) {
		t.Fatalf("expected phase %q in the error, got %q", phase, err.Error())
	}
	data := errors.DataFromError(err)
	if data["phase"] != string(phase) {
		t.Fatalf("want phase data %q, got %v", phase, data["phase"])
	}
	if data["status"] != string(status) {
		t.Fatalf("want status data %q, got %v", status, data["status"])
	}
}

// TestPhaseFingerprintProcessorIgnoresUnrelatedErrors guards the negative half
// of the processor: an error that carries no phase data must not be
// re-fingerprinted, or unrelated Sentry captures would be re-grouped.
func TestPhaseFingerprintProcessorIgnoresUnrelatedErrors(t *testing.T) {
	event := phaseFingerprintProcessor(
		&sentrygo.Event{},
		&sentrygo.EventHint{OriginalException: errors.New(context.Background(), "unrelated")},
	)
	if len(event.Fingerprint) != 0 {
		t.Fatalf("unrelated error must not be fingerprinted, got %v", event.Fingerprint)
	}
}

// recordingTransport captures events instead of sending them over the network.
type recordingTransport struct {
	events []*sentrygo.Event
}

func (t *recordingTransport) Configure(_ sentrygo.ClientOptions) {}

func (t *recordingTransport) SendEvent(event *sentrygo.Event) {
	t.events = append(t.events, event)
}

func (t *recordingTransport) Flush(_ time.Duration) bool { return true }

func (t *recordingTransport) FlushWithContext(_ context.Context) bool { return true }

func (t *recordingTransport) Close() {}

// TestPhaseFingerprintReachesSentryEvent proves the whole capture path: the
// processor and the libsentry tag enrichment both see the DataError carried by
// the failure, so the event a real Sentry client sends is self-describing and
// grouped per phase.
func TestPhaseFingerprintReachesSentryEvent(t *testing.T) {
	ctx := context.Background()
	sentrygo.AddGlobalEventProcessor(phaseFingerprintProcessor)

	transport := &recordingTransport{}
	client, err := libsentry.NewClient(ctx, sentrygo.ClientOptions{
		Dsn:       "https://key@example.com/1",
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("create sentry client: %v", err)
	}
	defer func() { _ = client.Close() }()

	phase := domain.TaskPhase("ai_review")
	failure := exitErrorForStatus(
		ctx,
		agentlib.AgentStatusFailed,
		phase,
		"ai_review: review surfaced blocking concerns: timeout",
	)
	client.CaptureException(
		failure,
		&sentrygo.EventHint{Context: ctx, OriginalException: failure},
		sentrygo.NewScope(),
	)

	if len(transport.events) != 1 {
		t.Fatalf("want 1 captured event, got %d", len(transport.events))
	}
	event := transport.events[0]
	if len(event.Fingerprint) != 3 ||
		event.Fingerprint[0] != "github-dark-factory-agent" ||
		event.Fingerprint[1] != "phase" ||
		event.Fingerprint[2] != "ai_review" {
		t.Fatalf("unexpected fingerprint: %v", event.Fingerprint)
	}
	if event.Tags["phase"] != "ai_review" {
		t.Fatalf("want phase tag ai_review, got %q", event.Tags["phase"])
	}
	if event.Tags["status"] != "failed" {
		t.Fatalf("want status tag failed, got %q", event.Tags["status"])
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
