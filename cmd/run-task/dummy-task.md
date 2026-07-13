---
task_type: dark-factory-implement
assignee: github-dark-factory-agent
status: in_progress
phase: planning
title: Implement approved spec on draft PR
repo: bborbe/REPLACE_ME
clone_url: https://github.com/bborbe/REPLACE_ME.git
ref: REPLACE_WITH_PR_HEAD_SHA
branch: REPLACE_WITH_PR_BRANCH
pr_number: 0
task_identifier: 00000000-0000-0000-0000-000000000000
---

# Dark-Factory Implement

Operator-readable header. The clone of the draft PR branch is the data source;
the planning phase clones it, validates the preconditions (ref == PR head,
`.dark-factory.yaml` present, an approved-not-completed spec in the PR diff, PR
is a draft), and writes `## Plan`.

Replace the frontmatter placeholders with a real draft PR before running:

    TASK_FILE=./dummy-task.md PHASE=planning go run ./cmd/run-task
