// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
)

// claudeProbeTimeout bounds the trivial `claude --print` liveness probe so a
// hung CLI cannot block the phase.
const claudeProbeTimeout = 60 * time.Second

// claudeUnauthMarkers are substrings claude prints when it has no usable
// login. backend: local runs claude as a subprocess inheriting the pod HOME;
// if the credential is not discoverable there, every prompt fails with one of
// these — the probe fails the task early instead.
var claudeUnauthMarkers = []string{
	"Not logged in",
	"Please run /login",
	"Invalid API key",
	"invalid api key",
}

// ClaudeProber runs a trivial claude invocation to confirm the CLI is
// authenticated before any prompt-bearing phase runs.
//
//counterfeiter:generate -o ../mocks/claude-prober.go --fake-name ClaudeProber . ClaudeProber
type ClaudeProber interface {
	// Probe returns nil when claude is authenticated and reachable, or an
	// error describing the failure otherwise.
	Probe(ctx context.Context) error
}

// NewClaudeProber constructs a ClaudeProber that shells out to `claude
// --print`. claudeConfigDir, when non-empty, is exported as CLAUDE_CONFIG_DIR
// so the probe reads the same login the agent will.
func NewClaudeProber(claudeConfigDir claudelib.ClaudeConfigDir) ClaudeProber {
	return &execClaudeProber{
		name:            "claude",
		args:            []string{"--print"},
		claudeConfigDir: string(claudeConfigDir),
		timeout:         claudeProbeTimeout,
	}
}

type execClaudeProber struct {
	name            string
	args            []string
	claudeConfigDir string
	timeout         time.Duration
}

// Probe runs `<name> <args...>` feeding "OK" on stdin. A non-zero exit or an
// unauth marker in the output is treated as "not authenticated".
func (p *execClaudeProber) Probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// #nosec G204 -- fixed argv (production sets name=claude); no task input reaches the command line
	cmd := exec.CommandContext(ctx, p.name, p.args...)
	cmd.Stdin = strings.NewReader("OK")
	cmd.Env = os.Environ()
	if p.claudeConfigDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+p.claudeConfigDir)
	}

	out, err := cmd.CombinedOutput()
	output := string(out)
	for _, marker := range claudeUnauthMarkers {
		if strings.Contains(output, marker) {
			return errors.Errorf(
				ctx,
				"claude not authenticated: %s",
				truncate(strings.TrimSpace(output)),
			)
		}
	}
	if err != nil {
		return errors.Wrapf(
			ctx,
			err,
			"claude probe failed: %s",
			truncate(strings.TrimSpace(output)),
		)
	}
	return nil
}
