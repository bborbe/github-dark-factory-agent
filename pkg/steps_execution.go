// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
)

// executionStep is the execution-phase step. STUB (Increment 2): the real
// dark-factory lifecycle driver (generate → approve → execute → verify →
// spec complete with backend: local) is not yet implemented. It compiles and
// wires so the planning phase is testable end to end.
type executionStep struct{}

// NewExecutionStep constructs the (stub) execution step.
func NewExecutionStep() agentlib.Step {
	return &executionStep{}
}

// Name implements agentlib.Step.
func (s *executionStep) Name() string { return "df-execution" }

// ShouldRun always returns true.
func (s *executionStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run returns Failed until Increment 2 supplies the real logic.
func (s *executionStep) Run(_ context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	return &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: "not yet implemented (Increment 2)",
	}, nil
}
