include ../../Makefile.variables

# No-op so the recursive `apply` sweep from the parent Makefile.docker doesn't
# choke on this directory. cmd/run-task is a local CLI for smoke tests, not a
# deployable artifact.
.PHONY: apply
apply:
	@echo "skip apply for cmd/run-task (local CLI, not deployed)"

TASK_FILE ?= dummy-task.md
PHASE ?= planning
CLAUDE_CONFIG_DIR ?= $(HOME)/.claude
AGENT_DIR ?= $(shell cd ../.. && pwd)/agent

# Local per-phase smoke run against the NEW 3-phase agent. Uses ambient gh +
# claude auth (no GitHub App). Point TASK_FILE at a task whose frontmatter
# names a real draft PR to exercise the planning preconditions end to end.
.PHONY: run-task
run-task: $(TASK_FILE)
	@go run -mod=mod . \
		-task-file=$(TASK_FILE) \
		-phase=$(PHASE) \
		-agent-dir=$(AGENT_DIR) \
		-claude-config-dir=$(CLAUDE_CONFIG_DIR) \
		-v=2
