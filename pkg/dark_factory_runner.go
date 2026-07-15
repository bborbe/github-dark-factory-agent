// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// darkFactoryBinary is the CLI the runner shells. Resolved from PATH (the
// claude-yolo image bakes it in at the pinned DARK_FACTORY_VERSION).
const darkFactoryBinary = "dark-factory"

// lifecyclePollInterval is how often RunLifecycle checks whether the prompt
// queue has drained. Local (filesystem) checks are cheap.
const lifecyclePollInterval = 3 * time.Second

// lifecycleTimeout bounds one lifecycle run. Generation + execution are claude
// calls; a trivial spec finishes in <2min, a large one can take many minutes.
// On timeout the daemon is stopped and RunLifecycle returns an error so the
// step escalates rather than hanging the Job.
const lifecycleTimeout = 30 * time.Minute

// darkFactoryRunner is the production ExecutionRunner: it shells `dark-factory`
// and `git` with the worktree as cwd, inheriting the pod env (HOME for claude
// auth, GH_TOKEN for push). It is exercised by the live smoke, not unit tests
// (the step's own tests fake this seam).
type darkFactoryRunner struct {
	binary       string
	pollInterval time.Duration
	timeout      time.Duration
}

// NewExecutionRunner constructs the production dark-factory/git lifecycle runner.
func NewExecutionRunner() ExecutionRunner {
	return &darkFactoryRunner{
		binary:       darkFactoryBinary,
		pollInterval: lifecyclePollInterval,
		timeout:      lifecycleTimeout,
	}
}

// RunLifecycle starts `dark-factory daemon` (backend:local) in the background,
// polls the on-disk spec/prompt state until every requested spec reaches a
// terminal state and the prompt queue drains, then stops the daemon and reports
// the outcome. The daemon is used (not one-shot `run`) because only the daemon's
// spec-watcher generates prompts from an approved spec; `run` merely drains an
// existing queue.
func (r *darkFactoryRunner) RunLifecycle(
	ctx context.Context,
	workdir string,
	specIDs, flags []string,
) (*LifecycleResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	daemon, err := r.startDaemon(runCtx, workdir, flags)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "start dark-factory daemon")
	}
	// Always stop the daemon before returning (idle at drain time = clean stop).
	defer r.stopDaemon(ctx, workdir, daemon)

	if err := r.waitForDrain(runCtx, workdir, specIDs); err != nil {
		return nil, errors.Wrap(ctx, err, "wait for lifecycle drain")
	}

	statuses := map[string]string{}
	for _, id := range specIDs {
		statuses[id] = readSpecStatus(ctx, workdir, id)
	}
	return &LifecycleResult{
		PromptsExecuted: countCompletedPrompts(workdir),
		SpecStatuses:    statuses,
	}, nil
}

// startDaemon launches `dark-factory daemon <flags>` in its own process group so
// the whole tree (including spawned claude subprocesses) can be signalled on stop.
func (r *darkFactoryRunner) startDaemon(
	ctx context.Context,
	workdir string,
	flags []string,
) (*exec.Cmd, error) {
	args := append([]string{"daemon"}, flags...)
	// #nosec G204 -- binary is a fixed constant; flags are package-constant literals
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ() // HOME for claude auth, PATH, GH_TOKEN — full pod env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Stream the daemon's stdout/stderr to our own (the container's) fd 1/2 so the
	// whole backend:local lifecycle — including the claude subprocesses the daemon
	// spawns — shows up in `kubectl logs`. RunLifecycle drives off the resulting
	// on-disk state, not this output, so nothing parses it; it is purely for
	// observability. Without it a silent daemon is a black box: a claude call
	// hanging on a no-TTY onboarding prompt looks identical to slow progress until
	// the lifecycle deadline fires. This writes to the process stdout/stderr, NOT a
	// file inside the repo, so it is never swept into the daemon's own commits.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	glog.V(2).Infof("execution: started dark-factory daemon pid=%d in %s", cmd.Process.Pid, workdir)
	return cmd, nil
}

// stopDaemon stops the daemon: `dark-factory kill` for a graceful lock-based
// shutdown, then a process-group kill as a hard fallback, then reaps the process.
func (r *darkFactoryRunner) stopDaemon(ctx context.Context, workdir string, cmd *exec.Cmd) {
	killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := r.run(killCtx, workdir, "kill"); err != nil {
		glog.V(2).Infof("execution: dark-factory kill returned: %v", err)
	}
	if cmd.Process != nil {
		// Negative pid = signal the whole process group.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = cmd.Wait()
}

// waitForDrain blocks until every requested spec is verifying/completed and no
// prompts remain in prompts/in-progress, or ctx (timeout) fires.
func (r *darkFactoryRunner) waitForDrain(
	ctx context.Context,
	workdir string,
	specIDs []string,
) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		if r.drained(ctx, workdir, specIDs) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx, ctx.Err(), "lifecycle did not drain before deadline")
		case <-ticker.C:
		}
	}
}

// drained reports whether every spec reached a terminal state AND no prompt is
// still in flight. "In flight" means either the generation INBOX (prompts/*.md
// at the repo root, a generated prompt awaiting the daemon's auto-approve audit)
// or prompts/in-progress (approved, awaiting execution). Checking the inbox is
// load-bearing: the daemon flips the spec to `verifying` while the auto-approve
// audit is still running, so without the inbox check drained() returns true
// mid-audit, stopDaemon SIGKILLs the in-flight audit, the prompt is never
// approved/executed, and the implementation file (e.g. the marker) is never
// created. A spec still in approved/generating/prompted also means work ongoing.
func (r *darkFactoryRunner) drained(ctx context.Context, workdir string, specIDs []string) bool {
	if hasInboxPrompts(workdir) || hasInProgressPrompts(workdir) {
		return false
	}
	for _, id := range specIDs {
		switch readSpecStatus(ctx, workdir, id) {
		case specStatusVerifying, specStatusCompleted:
			continue
		default:
			return false
		}
	}
	return true
}

// CompleteSpec drives `dark-factory spec complete <specID>` (verifying → completed).
func (r *darkFactoryRunner) CompleteSpec(ctx context.Context, workdir, specID string) error {
	if err := r.run(ctx, workdir, "spec", "complete", specID); err != nil {
		return errors.Wrapf(ctx, err, "dark-factory spec complete %s", specID)
	}
	return nil
}

// CommitSpecChanges stages and commits the working-tree changes under specs/ that
// `dark-factory spec complete` leaves behind. That CLI rewrites the tree
// (in-progress → completed) but does NOT git-commit — it runs after the daemon is
// stopped, so nothing commits it (unlike the daemon's per-prompt workflow:direct
// commits). PushBranch is a bare `git push HEAD`, so without this the spec
// completion is never pushed: the PR keeps an approved-not-completed spec and the
// watcher re-emits the task forever (self-trigger loop). A no-op when nothing is
// staged (e.g. dark-factory already committed the move, or no spec completed).
func (r *darkFactoryRunner) CommitSpecChanges(ctx context.Context, workdir string) error {
	if err := r.runGit(ctx, workdir, "add", "-A", "specs"); err != nil {
		return errors.Wrap(ctx, err, "git add specs")
	}
	if !r.hasStagedChanges(ctx, workdir) {
		return nil
	}
	if err := r.runGit(ctx, workdir, "commit", "-m", "dark-factory: complete spec(s)"); err != nil {
		return errors.Wrap(ctx, err, "git commit spec completions")
	}
	return nil
}

// runGit executes `git <args...>` in workdir, surfacing stderr on failure.
func (r *darkFactoryRunner) runGit(ctx context.Context, workdir string, args ...string) error {
	// #nosec G204 -- git is fixed; args are internal literals + validated spec ids
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(
			ctx,
			"git %s: %s",
			strings.Join(args, " "),
			strings.TrimSpace(stderr.String()),
		)
	}
	return nil
}

// hasStagedChanges reports whether the index holds staged changes
// (`git diff --cached --quiet` exits non-zero when it does).
func (r *darkFactoryRunner) hasStagedChanges(ctx context.Context, workdir string) bool {
	// #nosec G204 -- fixed git command, no user input
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	return cmd.Run() != nil
}

// PushBranch pushes the per-prompt commits on HEAD to origin/<branch>.
func (r *darkFactoryRunner) PushBranch(ctx context.Context, workdir, branch string) error {
	// #nosec G204 -- git is fixed; branch is validated frontmatter (branch-name shaped)
	cmd := exec.CommandContext(ctx, "git", "push", "origin", "HEAD:"+branch)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(
			ctx,
			"git push origin HEAD:%s: %s",
			branch,
			strings.TrimSpace(stderr.String()),
		)
	}
	return nil
}

// run executes `dark-factory <args...>` in workdir. Output is discarded — the
// step acts on the resulting on-disk state, not command stdout.
func (r *darkFactoryRunner) run(
	ctx context.Context,
	workdir string,
	args ...string,
) error {
	// #nosec G204 -- binary is a fixed constant; args are internal literals + validated spec ids
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(ctx, "%s %s: %s",
			r.binary, strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return nil
}

// hasInboxPrompts reports whether the generation inbox — prompts/*.md at the
// repo root — holds any prompt. A newly generated prompt lands here and stays
// until the daemon's auto-approve audit moves it to prompts/in-progress. The
// subdirectories (in-progress/completed/log) are not *.md files, so this glob
// matches only inbox prompts. See drained() for why the inbox is treated as
// work-in-flight.
func hasInboxPrompts(workdir string) bool {
	matches, _ := filepath.Glob(filepath.Join(workdir, "prompts", "*.md"))
	return len(matches) > 0
}

// hasInProgressPrompts reports whether prompts/in-progress holds any *.md file.
func hasInProgressPrompts(workdir string) bool {
	matches, _ := filepath.Glob(filepath.Join(workdir, "prompts", "in-progress", "*.md"))
	return len(matches) > 0
}

// countCompletedPrompts counts *.md files in prompts/completed.
func countCompletedPrompts(workdir string) int {
	matches, _ := filepath.Glob(filepath.Join(workdir, "prompts", "completed", "*.md"))
	return len(matches)
}

// readSpecStatus returns the frontmatter `status` of spec <id> found under
// specs/{in-progress,completed}. Returns "" when the file is absent/unreadable.
func readSpecStatus(ctx context.Context, workdir, id string) string {
	for _, dir := range []string{"in-progress", "completed"} {
		path := filepath.Join(workdir, "specs", dir, id+".md")
		content, err := os.ReadFile(
			path,
		) // #nosec G304 -- path built from validated worktree + spec id
		if err != nil {
			continue
		}
		parsed, err := agentlib.ParseMarkdown(ctx, string(content))
		if err != nil {
			continue
		}
		if status, ok := parsed.Frontmatter.String("status"); ok {
			return strings.TrimSpace(status)
		}
	}
	return ""
}
