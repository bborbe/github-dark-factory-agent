// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// The ai_review step injects a read-only claudelib.ClaudeRunner as a seam so
// tests can fake the diff-vs-spec verdict without a real claude binary. The
// interface lives in an external package, so the directive names it fully.
//
//counterfeiter:generate -o ../mocks/claude-runner.go --fake-name ClaudeRunner github.com/bborbe/agent/claude.ClaudeRunner
