// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the agent-claude binary.
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
	"github.com/bborbe/vault-cli/pkg/domain"

	"github.com/bborbe/agent-claude/pkg/prompts"
)

const serviceName = "agent-claude"

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

// CreateSyncProducer creates a Kafka sync producer.
func CreateSyncProducer(
	ctx context.Context,
	brokers libkafka.Brokers,
) (libkafka.SyncProducer, error) {
	return libkafka.NewSyncProducerWithName(ctx, brokers, serviceName)
}

// CreateKafkaResultDeliverer creates a ResultDeliverer that publishes task
// updates to Kafka via CQRS commands. Uses the passthrough content generator
// — the agent framework's StepRunner already produces the full marshaled
// task in result.Output; the deliverer publishes it as-is and overrides
// status/phase frontmatter based on the result Status.
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
// output back to a markdown file (local CLI mode). Uses the passthrough
// content generator (same rationale as Kafka).
func CreateFileResultDeliverer(filePath string) agentlib.ResultDeliverer {
	return delivery.NewFileResultDeliverer(
		delivery.NewPassthroughContentGenerator(),
		filePath,
	)
}

// CreateAgent assembles the full 3-phase claude agent. Single Claude step
// shared across planning / in_progress / ai_review preserves the existing
// CRD trigger.phases behavior — every phase runs Claude once and emits
// done.
func CreateAgent(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	allowedTools claudelib.AllowedTools,
	model claudelib.ClaudeModel,
	claudeEnv map[string]string,
	envContext map[string]string,
) *agentlib.Agent {
	return CreateAgentFromRunner(
		CreateClaudeRunner(claudeConfigDir, agentDir, allowedTools, model, claudeEnv),
		envContext,
	)
}

// CreateAgentFromRunner builds the 3-phase claude agent given a pre-constructed
// ClaudeRunner. Used by CreateAgentProvider to share one runner across the
// domain agent and the healthcheck-Claude liveness agent.
func CreateAgentFromRunner(
	runner claudelib.ClaudeRunner,
	envContext map[string]string,
) *agentlib.Agent {
	step := claudelib.NewAgentStep(claudelib.AgentStepConfig{
		Name:          "claude-task",
		Runner:        runner,
		Instructions:  prompts.BuildInstructions(),
		EnvContext:    envContext,
		OutputSection: "## Result",
		NextPhase:     "done",
	})
	return agentlib.NewAgent(
		agentlib.NewPhase("planning", step),
		agentlib.NewPhase(domain.TaskPhaseExecution, step),
		agentlib.NewPhase("ai_review", step),
	)
}

// CreateAgentProvider wires the per-task-type dispatch table for agent-claude.
// Returns lib.AgentProvider — main.go calls Get(ctx, taskType) to select the
// appropriate *Agent. Pure plumbing; no conditional, no error.
//
// TaskTypeLLM routes to the existing 3-phase domain agent. TaskTypeHealthcheck
// and TaskTypeOAuthProbe (transition alias) both route to the shared
// healthcheck-Claude liveness agent, reusing the same ClaudeRunner.
func CreateAgentProvider(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	allowedTools claudelib.AllowedTools,
	model claudelib.ClaudeModel,
	claudeEnv map[string]string,
	envContext map[string]string,
) agentlib.AgentProvider {
	runner := CreateClaudeRunner(claudeConfigDir, agentDir, allowedTools, model, claudeEnv)
	domainAgent := CreateAgentFromRunner(runner, envContext)
	livenessAgent := healthcheck.NewAgent(healthcheck.NewClaudeStep(runner))
	return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
		agentlib.TaskTypeLLM:         domainAgent,
		agentlib.TaskTypeHealthcheck: livenessAgent,
		agentlib.TaskTypeOAuthProbe:  livenessAgent,
	})
}
