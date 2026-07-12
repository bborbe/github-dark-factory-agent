include ../../Makefile.variables
include ../../common.env

# No-op so the recursive `apply` sweep from the parent Makefile.docker doesn't
# choke on this directory. cmd/run-task is a local CLI for smoke tests, not a
# deployable artifact.
.PHONY: apply
apply:
	@echo "skip apply for cmd/run-task (local CLI, not deployed)"


TASK_FILE ?= dummy-task.md
BRANCH := dev
CLAUDE_CONFIG_DIR ?= $(HOME)/.claude-agent
AGENT_DIR ?= $(shell cd ../.. && pwd)/agent
ALLOWED_TOOLS ?= Grep,Read

# MiniMax routing defaults. Token is fetched live from the personal teamvault
# (secret key MOPmQL) so it never lands on disk; requires `teamvault-password`
# from github.com/bborbe/teamvault-utils. Override any var to point elsewhere.
ANTHROPIC_BASE_URL ?= https://api.minimax.io/anthropic
ANTHROPIC_AUTH_TOKEN ?= $(shell teamvault-password --teamvault-config $(HOME)/.teamvault.json --teamvault-key MOPmQL 2>/dev/null)
ANTHROPIC_MODEL ?= MiniMax-M2.7-highspeed

generate-dummy-task:
	@echo '---' > $(TASK_FILE)
	@echo 'status: in_progress' >> $(TASK_FILE)
	@echo 'phase: planning' >> $(TASK_FILE)
	@echo 'title: Dummy Claude Task' >> $(TASK_FILE)
	@echo 'assignee: claude-agent' >> $(TASK_FILE)
	@echo '---' >> $(TASK_FILE)
	@echo '' >> $(TASK_FILE)
	@echo 'Report which language model is processing this task. Return a JSON object with these exact fields:' >> $(TASK_FILE)
	@echo '' >> $(TASK_FILE)
	@echo '- `model`: the model name as you know yourself (e.g. "claude-sonnet-4-6", "MiniMax-M2.7", etc.)' >> $(TASK_FILE)
	@echo '- `provider`: the provider/company that built you ("Anthropic", "MiniMax", etc.)' >> $(TASK_FILE)
	@echo '- `notes`: one sentence explaining how you identified yourself' >> $(TASK_FILE)
	@echo '' >> $(TASK_FILE)
	@echo 'Return ONLY the JSON object, no prose around it.' >> $(TASK_FILE)
	@echo "generated $(TASK_FILE)"

run-dummy-task: $(TASK_FILE)
	@go run -mod=mod . \
		-task-file=$(TASK_FILE) \
		-branch=$(BRANCH) \
		-agent-dir=$(AGENT_DIR) \
		-claude-config-dir=$(CLAUDE_CONFIG_DIR) \
		-anthropic-model='$(ANTHROPIC_MODEL)' \
		-allowed-tools=$(ALLOWED_TOOLS) \
		-anthropic-base-url='$(ANTHROPIC_BASE_URL)' \
		-anthropic-auth-token='$(ANTHROPIC_AUTH_TOKEN)' \
		-v=2
