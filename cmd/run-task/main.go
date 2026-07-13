// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command run-task is the local-CLI entry point for github-dark-factory-agent.
//
// Reads a markdown task file from disk, runs the selected phase of the 3-phase
// agent against it, and writes the updated content back to the same file.
// Mirrors the Kafka entry point (../../main.go) but uses file I/O instead of
// Kafka/CQRS.
//
// Auth is kept deliberately simple for local testing: it uses ambient `gh`
// CLI auth (or GH_TOKEN if set) and ambient claude auth (the user's ~/.claude)
// — no GitHub App required. Point it at a real draft PR (planning phase needs
// the GitHub REST API) or a local sandbox repo.
//
//	TASK_FILE=./dummy-task.md PHASE=planning go run ./cmd/run-task
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/agent/envparse"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-dark-factory-agent/pkg/factory"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy" display:"length"`

	// Claude Code CLI configuration
	ClaudeConfigDir claudelib.ClaudeConfigDir `required:"false" arg:"claude-config-dir" env:"CLAUDE_CONFIG_DIR" usage:"Claude Code config directory" default:"~/.claude"`

	// Agent directory (contains .claude/ with CLAUDE.md and commands)
	AgentDir claudelib.AgentDir `required:"false" arg:"agent-dir" env:"AGENT_DIR" usage:"Agent directory with .claude/ config" default:"agent"`

	// Workdir paths for bare-clone cache and per-task worktrees. Empty defaults
	// to ~/.cache/github-dark-factory-agent/{repos,work} for local runs.
	ReposPath string `required:"false" arg:"repos-path" env:"REPOS_PATH" usage:"Root path for bare-clone cache (default: ~/.cache/github-dark-factory-agent/repos)"`
	WorkPath  string `required:"false" arg:"work-path"  env:"WORK_PATH"  usage:"Root path for per-task worktrees (default: ~/.cache/github-dark-factory-agent/work)"`

	// GitHub token. Empty → resolved from ambient `gh auth token` at runtime.
	GhToken string `required:"false" arg:"gh-token" env:"GH_TOKEN" usage:"GitHub token; empty falls back to ambient gh auth token" display:"length"`

	// Environment
	Branch base.Branch `required:"false" arg:"branch" env:"BRANCH" usage:"branch" default:"dev"`

	// Phase to run (framework requires explicit phase)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"execution"`

	// Task file for local development
	TaskFilePath string `required:"true" arg:"task-file" env:"TASK_FILE" usage:"Path to the markdown task file"`

	// Anthropic-compatible provider routing (forwarded to the claude subprocess).
	AnthropicBaseURL   string                `required:"false" arg:"anthropic-base-url"   env:"ANTHROPIC_BASE_URL"   usage:"Anthropic-compatible API base URL"`
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"length"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                  default:"sonnet"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	taskContent, err := os.ReadFile(
		a.TaskFilePath,
	) // #nosec G304 -- filePath from trusted CLI input
	if err != nil {
		return errors.Wrapf(ctx, err, "read task file: %s", a.TaskFilePath)
	}

	reposPath, workPath, err := a.resolveCachePaths(ctx)
	if err != nil {
		return err
	}
	ghToken := a.resolveGhToken(ctx)

	claudeEnv := envparse.KeyValuePairs("")
	if claudeEnv == nil {
		claudeEnv = map[string]string{}
	}
	if a.AnthropicBaseURL != "" {
		claudeEnv["ANTHROPIC_BASE_URL"] = a.AnthropicBaseURL
	}
	if a.AnthropicAuthToken != "" {
		claudeEnv["ANTHROPIC_AUTH_TOKEN"] = a.AnthropicAuthToken
	}
	if a.AnthropicModel != "" {
		claudeEnv["ANTHROPIC_MODEL"] = a.AnthropicModel.String()
	}

	deliverer := factory.CreateFileResultDeliverer(a.TaskFilePath)
	repoManager := factory.CreateRepoManager(reposPath, workPath, ghToken)
	githubClient := factory.CreateGitHubClient(ghToken)
	claudeProber := factory.CreateClaudeProber(a.ClaudeConfigDir)

	agent := factory.CreateAgent(
		a.ClaudeConfigDir,
		a.AgentDir,
		a.AnthropicModel,
		ghToken,
		claudeEnv,
		repoManager,
		githubClient,
		claudeProber,
	)

	result, err := agent.Run(ctx, a.Phase, string(taskContent), deliverer)
	if err != nil {
		return errors.Wrap(ctx, err, "agent run failed")
	}
	return agentlib.PrintResult(ctx, result)
}

// resolveCachePaths fills in defaults for ReposPath/WorkPath when unset
// (~/.cache/github-dark-factory-agent/{repos,work}). The pod entry point
// requires explicit /repos and /work mounts; local CLI usage benefits from a
// default.
func (a *application) resolveCachePaths(ctx context.Context) (string, string, error) {
	reposPath := a.ReposPath
	workPath := a.WorkPath
	if reposPath != "" && workPath != "" {
		return reposPath, workPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", errors.Wrap(ctx, err, "resolve user home dir")
	}
	if reposPath == "" {
		reposPath = filepath.Join(home, ".cache", "github-dark-factory-agent", "repos")
	}
	if workPath == "" {
		workPath = filepath.Join(home, ".cache", "github-dark-factory-agent", "work")
	}
	return reposPath, workPath, nil
}

// resolveGhToken returns the configured GhToken, or falls back to `gh auth
// token` for ambient local auth. An empty result is returned when neither is
// available (the gh-token preflight then escalates cleanly).
func (a *application) resolveGhToken(ctx context.Context) string {
	if a.GhToken != "" {
		return a.GhToken
	}
	// #nosec G204 -- fixed argv; no user-controlled input reaches the command line
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		glog.V(2).
			Infof("run-task: `gh auth token` unavailable (%v) — proceeding without a token", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
