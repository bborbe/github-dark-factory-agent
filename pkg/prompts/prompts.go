// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package prompts provides embedded prompt fragments for the Claude agent.
package prompts

import (
	_ "embed"

	claudelib "github.com/bborbe/agent/claude"
)

//go:embed workflow.md
var workflow string

//go:embed output-format.md
var outputFormat string

// BuildInstructions assembles the full agent prompt from embedded modules.
// Each section is wrapped in XML tags for clear separation.
func BuildInstructions() claudelib.Instructions {
	return claudelib.Instructions{
		{Name: "workflow", Content: workflow},
		{Name: "output-format", Content: outputFormat},
	}
}
