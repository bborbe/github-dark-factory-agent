// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command github-dark-factory-agent is the canonical AI-heavy agent: one Claude
// invocation per phase, all logic in the prompt + allowed tools.
//
// This binary is the Kafka entry point — spawned as a K8s Job by
// task/executor with TASK_CONTENT + TASK_ID + PHASE + KAFKA_BROKERS env.
// For local CLI mode (file-based), see cmd/run-task/main.go.
//
// Reference implementation for AI-heavy agents using the agent framework
// (lib.NewAgent + claude.NewAgentStep). Other agents (trade-analysis,
// pr-reviewer) follow the same shape — copy this main.go and swap
// prompts/tools.
package main

import (
	"context"
	"os"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	"github.com/bborbe/agent/envparse"
	libmetrics "github.com/bborbe/agent/metrics"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	libkafka "github.com/bborbe/kafka"
	githubapp "github.com/bborbe/maintainer/githubapp"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"

	"github.com/bborbe/github-dark-factory-agent/pkg/factory"
	"github.com/bborbe/github-dark-factory-agent/pkg/githubauth"
)

// agentName is the identity string used for Prometheus metric grouping and logging.
const agentName = "github-dark-factory-agent"

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy" display:"length"`

	// Claude Code CLI configuration
	ClaudeConfigDir claudelib.ClaudeConfigDir `required:"false" arg:"claude-config-dir" env:"CLAUDE_CONFIG_DIR" usage:"Claude Code config directory"`

	// Agent directory (contains .claude/ with CLAUDE.md and commands)
	AgentDir claudelib.AgentDir `required:"false" arg:"agent-dir" env:"AGENT_DIR" usage:"Agent directory with .claude/ config" default:"agent"`

	// Allowed tools (comma-separated)
	AllowedToolsRaw string `required:"false" arg:"allowed-tools" env:"ALLOWED_TOOLS" usage:"Comma-separated list of allowed tools"`

	// Task content from agent pipeline
	TaskContent string `required:"true" arg:"task-content" env:"TASK_CONTENT" usage:"Raw task markdown from vault"`

	// Environment context passed to prompt (comma-separated KEY=VALUE pairs)
	EnvContextRaw string `required:"false" arg:"env-context" env:"ENV_CONTEXT" usage:"Comma-separated KEY=VALUE pairs for prompt context"`

	// Environment variables passed to Claude CLI process (comma-separated KEY=VALUE pairs).
	// Use this for ad-hoc / less-common env vars. The three load-bearing Anthropic provider
	// vars below have dedicated arg slots so they don't have to be packed into this string.
	ClaudeEnvRaw string `required:"false" arg:"claude-env" env:"CLAUDE_ENV" usage:"Comma-separated KEY=VALUE pairs for Claude CLI environment"`

	// Anthropic-compatible provider routing. Setting AnthropicBaseURL + AnthropicAuthToken
	// routes the claude CLI to an alt-provider (e.g. MiniMax via https://api.minimax.io/anthropic).
	// AnthropicModel drives both the `--model` CLI flag and the ANTHROPIC_MODEL env var seen by
	// the claude subprocess. Non-empty values override the same keys in ClaudeEnvRaw.
	AnthropicBaseURL   string                `required:"false" arg:"anthropic-base-url"   env:"ANTHROPIC_BASE_URL"   usage:"Anthropic-compatible API base URL"`
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"password"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                    default:"sonnet"`

	// Branch for Kafka result delivery
	Branch base.Branch `required:"false" arg:"branch" env:"BRANCH" usage:"branch"`

	// TopicPrefix is an explicit Kafka topic prefix, independent of Branch.
	TopicPrefix base.TopicPrefix `required:"false" arg:"topic-prefix" env:"TOPIC_PREFIX" usage:"Explicit Kafka topic prefix; empty means unprefixed topics"`

	// Phase to run (framework requires explicit phase)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"execution"`

	// GitHub token for authenticated clones + REST API calls (planning phase).
	// Used as the raw fallback credential when GitHub App creds are absent
	// (local cmd/run-task path where the operator exports `gh auth token`).
	GhToken string `required:"false" arg:"gh-token" env:"GH_TOKEN" usage:"GitHub token for clone + PR inspection (raw fallback when App creds unset)" display:"length"`

	// GitHub App authentication. When APP_ID, INSTALLATION_ID, and a PEM (file
	// or inline) are all set, the pod mints a short-lived installation access
	// token at startup and forwards it to every git/gh subprocess as GH_TOKEN.
	// The cluster pod sets these (GH_TOKEN is empty there); locally they are
	// absent and the agent falls back to the raw GhToken above.
	AppID          int64  `required:"false" arg:"app-id"          env:"APP_ID"          usage:"GitHub App ID (numeric); enables App auth when set with INSTALLATION_ID + PEM"`
	InstallationID int64  `required:"false" arg:"installation-id" env:"INSTALLATION_ID" usage:"GitHub App Installation ID (numeric)"`
	PEMKeyFile     string `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"    usage:"Path to the GitHub App private key (PEM file mounted from k8s Secret)"`
	PEMKey         string `required:"false" arg:"pem-key"         env:"PEM_KEY"         usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE" display:"length"`

	// Workdir paths for bare-clone cache and per-task worktrees.
	ReposPath string `required:"false" arg:"repos-path" env:"REPOS_PATH" usage:"Root path for bare-clone cache"   default:"/repos"`
	WorkPath  string `required:"false" arg:"work-path"  env:"WORK_PATH"  usage:"Root path for per-task worktrees" default:"/work"`

	// Kafka delivery (optional — only active when TASK_ID is set)
	KafkaBrokers libkafka.Brokers        `required:"false" arg:"kafka-brokers" env:"KAFKA_BROKERS" usage:"Comma separated list of Kafka brokers"`
	TaskID       agentlib.TaskIdentifier `required:"false" arg:"task-id"       env:"TASK_ID"       usage:"Agent task identifier for publishing results back to task controller"`

	PushgatewayURL string `required:"false" arg:"pushgateway-url" env:"PUSHGATEWAY_URL" usage:"Prometheus PushGateway URL"          default:"http://pushgateway:9090"`
	TaskType       string `required:"false" arg:"task-type"       env:"TASK_TYPE"       usage:"Task type label for metric grouping" default:"unknown"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	registry := prometheus.NewRegistry()
	jobMetrics := libmetrics.NewJobMetrics(registry, libtime.NewCurrentDateTime())
	pusher := push.New(a.PushgatewayURL, libmetrics.BuildJobMetricsName(agentName)).
		Grouping("agent", agentName).
		Grouping("task_type", a.TaskType).
		Collector(registry)
	defer func() {
		if err := pusher.PushContext(ctx); err != nil {
			glog.Warningf("prometheus push failed: %v", err)
			return
		}
		glog.V(2).Infof("prometheus push completed")
	}()
	start := libtime.NewCurrentDateTime().Now().Time()

	glog.V(2).Infof("github-dark-factory-agent started phase=%s", a.Phase)

	// Resolve the GitHub credential (App IAT or raw GH_TOKEN fallback) and make
	// it usable by every git/gh subprocess. See prepareAuth.
	resolvedToken, err := a.prepareAuth(ctx)
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return err
	}

	deliverer := delivery.NewNoopResultDeliverer()
	if a.TaskID != "" {
		if len(a.KafkaBrokers) == 0 {
			jobMetrics.RecordRun(agentlib.AgentStatusFailed)
			jobMetrics.RecordDuration(time.Since(start))
			return errors.Errorf(ctx, "KAFKA_BROKERS must be set when TASK_ID is set")
		}
		syncProducer, err := factory.CreateSyncProducer(ctx, a.KafkaBrokers)
		if err != nil {
			jobMetrics.RecordRun(agentlib.AgentStatusFailed)
			jobMetrics.RecordDuration(time.Since(start))
			return errors.Wrap(ctx, err, "create sync producer")
		}
		defer func() {
			if err := syncProducer.Close(); err != nil {
				glog.Warningf("close sync producer failed: %v", err)
			}
		}()
		deliverer = factory.CreateKafkaResultDeliverer(
			syncProducer, a.TopicPrefix, a.TaskID, a.TaskContent,
			libtime.NewCurrentDateTime(),
		)
	}

	claudeEnv := a.buildClaudeEnv(resolvedToken)

	repoManager := factory.CreateRepoManager(a.ReposPath, a.WorkPath, resolvedToken)
	githubClient := factory.CreateGitHubClient(resolvedToken)
	claudeProber := factory.CreateClaudeProber(a.ClaudeConfigDir)
	provider := factory.CreateAgentProvider(
		a.ClaudeConfigDir,
		a.AgentDir,
		a.AnthropicModel,
		resolvedToken,
		claudeEnv,
		repoManager,
		githubClient,
		claudeProber,
	)
	agent, err := provider.Get(ctx, agentlib.TaskType(a.TaskType))
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return errors.Wrap(ctx, err, "select agent for task_type")
	}

	result, err := agent.Run(ctx, a.Phase, a.TaskContent, deliverer)
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return errors.Wrap(ctx, err, "agent run failed")
	}
	jobMetrics.RecordRun(result.Status)
	jobMetrics.RecordDuration(time.Since(start))
	return agentlib.PrintResult(ctx, result)
}

// buildClaudeEnv assembles the Claude CLI subprocess env from the raw env
// pairs plus the Anthropic provider-routing overrides. resolvedToken, when
// non-empty, is threaded in as GH_TOKEN — the ai_review phase's Claude
// subprocess shells `gh pr view/diff`, and the runner strips pod env to an
// allowlist, so the token must be passed explicitly (os.Setenv in prepareAuth
// covers the dark-factory daemon path, not the Claude runner).
func (a *application) buildClaudeEnv(resolvedToken string) map[string]string {
	claudeEnv := envparse.KeyValuePairs(a.ClaudeEnvRaw)
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
	if resolvedToken != "" {
		claudeEnv["GH_TOKEN"] = resolvedToken
	}
	return claudeEnv
}

// prepareAuth resolves the GitHub credential and makes it usable by every
// git/gh subprocess: it exports GH_TOKEN (covering the dark-factory daemon,
// which inherits os.Environ() for its `git push`) and installs git's credential
// helper via `gh auth setup-git`. Returns the resolved token for the
// RepoManager / GitHub client / gh-token preflight wiring.
func (a *application) prepareAuth(ctx context.Context) (string, error) {
	resolvedToken, err := a.resolveAuth(ctx)
	if err != nil {
		return "", err
	}
	// resolvedToken is always non-empty here (resolveAuth errors otherwise), but
	// guard defensively — an empty GH_TOKEN would mislead the daemon subprocess.
	if resolvedToken != "" {
		if err := os.Setenv("GH_TOKEN", resolvedToken); err != nil {
			return "", errors.Wrap(ctx, err, "export GH_TOKEN")
		}
	}
	if err := githubauth.NewGhAuthSetupGit(resolvedToken).Setup(ctx); err != nil {
		return "", errors.Wrap(ctx, err, "gh auth setup-git")
	}
	return resolvedToken, nil
}

// resolveAuth resolves the GitHub credential forwarded to every git/gh
// subprocess. It prefers GitHub App authentication: when APP_ID,
// INSTALLATION_ID, and a PEM (file or inline) are all set, it mints a
// short-lived installation access token via githubapp.MintIAT. Otherwise it
// falls back to the raw GH_TOKEN input — the local cmd/run-task path where the
// operator exports `gh auth token`. When neither is configured it returns a
// clear error, since the agent cannot clone or push without a credential.
//
// The token is a runtime value (not a config input), so it is returned to the
// caller rather than stored on the argument-parsed application struct —
// argument/v2 panics when reflecting over unexported struct fields at startup.
func (a *application) resolveAuth(ctx context.Context) (string, error) {
	hasPEMFile := a.PEMKeyFile != ""
	hasPEMContent := a.PEMKey != ""
	useGitHubApp := a.AppID != 0 && a.InstallationID != 0 && (hasPEMFile || hasPEMContent)
	if useGitHubApp {
		appCfg := githubapp.Config{AppID: a.AppID, InstallationID: a.InstallationID}
		if hasPEMFile {
			appCfg.PEMPath = a.PEMKeyFile
		} else {
			appCfg.PEM = []byte(a.PEMKey)
		}
		// Startup auth must not be killed by the task's work deadline: mint on a
		// fresh bounded context (keeps ctx values, drops the caller's
		// deadline/cancel) so a near-deadline task can't fail the token mint.
		mintCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		iat, err := githubapp.MintIAT(mintCtx, appCfg)
		if err != nil {
			return "", errors.Wrap(ctx, err, "mint github app iat")
		}
		glog.V(2).Infof(
			"github-dark-factory-agent auth mode=github-app app_id=%d installation_id=%d",
			a.AppID, a.InstallationID,
		)
		return iat, nil
	}
	if a.GhToken != "" {
		glog.V(2).Infof("github-dark-factory-agent auth mode=gh-token (raw GH_TOKEN fallback)")
		return a.GhToken, nil
	}
	return "", errors.Errorf(
		ctx,
		"github-dark-factory-agent auth: no credentials configured — set GitHub App creds (APP_ID, INSTALLATION_ID, PEM_KEY_FILE or PEM_KEY) or GH_TOKEN",
	)
}
