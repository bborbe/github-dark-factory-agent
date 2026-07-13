// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package prompts provides embedded prompt fragments for the agent.
//
// Increment 1: planning and execution are prompt-free (pure-Go planning; the
// dark-factory lifecycle drives execution). The only Claude phase is ai_review
// (Increment 3), whose prompt is added then. BuildInstructions currently
// returns an empty set so the package compiles and callers stay stable.
package prompts

import (
	claudelib "github.com/bborbe/agent/claude"
)

// BuildInstructions returns the agent prompt fragments. Empty until the
// ai_review prompt lands in Increment 3.
func BuildInstructions() claudelib.Instructions {
	return claudelib.Instructions{}
}
