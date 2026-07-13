// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the github-dark-factory-agent binary.
//
// All factory functions follow the Create* prefix convention and contain
// zero business logic — they compose constructors with config.
package factory

import (
	"context"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	healthcheck "github.com/bborbe/agent/healthcheck"
	"github.com/bborbe/cqrs/base"
	libkafka "github.com/bborbe/kafka"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"

	dfpkg "github.com/bborbe/github-dark-factory-agent/pkg"
	"github.com/bborbe/github-dark-factory-agent/pkg/git"
)

const serviceName = "github-dark-factory-agent"

// taskTypeDarkFactoryImplement is the agent-lib TaskType literal for this
// agent's domain task. No constant exists in agent-lib for this value, so we
// cast it locally (mirrors the releaser's taskTypeGitHubRelease). Keep the
// literal exactly "dark-factory-implement" — the watcher emits it verbatim and
// the CRD trigger.task_type field must match.
var taskTypeDarkFactoryImplement = agentlib.TaskType("dark-factory-implement")

// Per-phase Claude allowed-tools sets. Planning and execution are prompt-free
// in Increment 1 (pure-Go planning; stub execution), so their tool sets are
// empty. The ai_review scope (Increment 3) is read-only inspection.
var (
	planningTools  = claudelib.AllowedTools{}
	executionTools = claudelib.AllowedTools{}
	reviewTools    = claudelib.AllowedTools{
		"Read", "Grep",
		"Bash(gh pr view:*)", "Bash(gh pr diff:*)",
	}
)

// CreateClaudeRunner constructs a ClaudeRunner pre-configured with tools,
// model, working directory, and CLI environment.
func CreateClaudeRunner(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	allowedTools claudelib.AllowedTools,
	model claudelib.ClaudeModel,
	env map[string]string,
) claudelib.ClaudeRunner {
	return claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
		ClaudeConfigDir:  claudeConfigDir,
		AllowedTools:     allowedTools,
		Model:            model,
		WorkingDirectory: agentDir,
		Env:              env,
	})
}

// CreateRepoManager wires the bare-clone / worktree manager with cache paths
// and the GitHub token used for authenticated HTTPS clones. Pure plumbing.
func CreateRepoManager(reposPath, workPath, ghToken string) git.RepoManager {
	return git.NewRepoManager(git.WorkdirConfig{ReposPath: reposPath, WorkPath: workPath}, ghToken)
}

// CreateGitHubClient wires the GitHub REST client used by the planning phase.
func CreateGitHubClient(ghToken string) dfpkg.GitHubClient {
	return dfpkg.NewGitHubClient(ghToken)
}

// CreateClaudeProber wires the claude-auth preflight prober.
func CreateClaudeProber(claudeConfigDir claudelib.ClaudeConfigDir) dfpkg.ClaudeProber {
	return dfpkg.NewClaudeProber(claudeConfigDir)
}

// CreateSyncProducer creates a Kafka sync producer.
func CreateSyncProducer(
	ctx context.Context,
	brokers libkafka.Brokers,
) (libkafka.SyncProducer, error) {
	return libkafka.NewSyncProducerWithName(ctx, brokers, serviceName)
}

// CreateKafkaResultDeliverer creates a ResultDeliverer that publishes task
// updates to Kafka via CQRS commands. Uses the passthrough content generator.
func CreateKafkaResultDeliverer(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
	taskID agentlib.TaskIdentifier,
	originalContent string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.ResultDeliverer {
	return delivery.NewKafkaResultDeliverer(
		syncProducer,
		topicPrefix,
		taskID,
		originalContent,
		delivery.NewPassthroughContentGenerator(),
		currentDateTime,
	)
}

// CreateFileResultDeliverer creates a ResultDeliverer that writes the agent's
// output back to a markdown file (local CLI mode).
func CreateFileResultDeliverer(filePath string) agentlib.ResultDeliverer {
	return delivery.NewFileResultDeliverer(
		delivery.NewPassthroughContentGenerator(),
		filePath,
	)
}

// CreateAgent assembles the three distinct phases:
//
//   - planning:  claude-auth + gh-token preflight + pure-Go spec scan → ## Plan
//   - execution: claude-auth preflight + dark-factory backend:local lifecycle → ## Result
//   - ai_review: read-only Claude diff-vs-spec verifier → ## Review → human_review
//
// All three phases are implemented domain logic.
func CreateAgent(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	githubClient dfpkg.GitHubClient,
	claudeProber dfpkg.ClaudeProber,
) *agentlib.Agent {
	_ = planningTools
	_ = executionTools

	claudeAuth := dfpkg.NewClaudeAuthStep(claudeProber)
	ghTokenCheck := dfpkg.NewGHTokenCheckStep(ghToken)
	planning := dfpkg.NewPlanningStep(repoManager, githubClient)
	execution := dfpkg.NewExecutionStep(repoManager, dfpkg.NewExecutionRunner())
	reviewRunner := CreateClaudeRunner(claudeConfigDir, agentDir, reviewTools, model, env)
	review := dfpkg.NewAIReviewStep(githubClient, repoManager, reviewRunner)

	return agentlib.NewAgent(
		agentlib.NewPhase(domain.TaskPhasePlanning, claudeAuth, ghTokenCheck, planning),
		agentlib.NewPhase(domain.TaskPhaseExecution, claudeAuth, execution),
		agentlib.NewPhase(domain.TaskPhaseAIReview, review),
	)
}

// CreateAgentProvider wires the per-task-type dispatch table.
//   - task_type: dark-factory-implement → the 3-phase domain agent
//   - task_type: healthcheck / oauth-probe → shared liveness agent
//
// Pure plumbing; no conditional, no error.
func CreateAgentProvider(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	githubClient dfpkg.GitHubClient,
	claudeProber dfpkg.ClaudeProber,
) agentlib.AgentProvider {
	domainAgent := CreateAgent(
		claudeConfigDir,
		agentDir,
		model,
		ghToken,
		env,
		repoManager,
		githubClient,
		claudeProber,
	)
	healthcheckRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		claudelib.AllowedTools{},
		model,
		env,
	)
	livenessAgent := healthcheck.NewAgent(healthcheck.NewClaudeStep(healthcheckRunner))
	return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
		taskTypeDarkFactoryImplement: domainAgent,
		agentlib.TaskTypeHealthcheck: livenessAgent,
		agentlib.TaskTypeOAuthProbe:  livenessAgent,
	})
}
