// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
)

// aiReviewStep is the ai_review-phase step. STUB (Increment 3): the read-only
// Claude verifier that writes ## Review and routes to human_review is not yet
// implemented. It compiles and wires so the agent builds.
type aiReviewStep struct{}

// NewAIReviewStep constructs the (stub) ai_review step.
func NewAIReviewStep() agentlib.Step {
	return &aiReviewStep{}
}

// Name implements agentlib.Step.
func (s *aiReviewStep) Name() string { return "df-ai-review" }

// ShouldRun always returns true.
func (s *aiReviewStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run returns Failed until Increment 3 supplies the real logic.
func (s *aiReviewStep) Run(_ context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	return &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: "not yet implemented (Increment 3)",
	}, nil
}
