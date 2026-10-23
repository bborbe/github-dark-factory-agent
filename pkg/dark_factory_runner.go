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
//
// Default is 90m (raised from 30m 2026-08-21 after real 2-prompt feature runs
// hit the deadline mid-execution: generation ~7m + per-prompt execute 10-15m
// + fix loops exceeds 30m). Overridable per deployment via LIFECYCLE_TIMEOUT
// (duration, e.g. "90m") and per task via `lifecycle_timeout_minutes:` in the
// task frontmatter (the execution step resolves it and passes it down).
const lifecycleTimeout = 90 * time.Minute

// The statuses dark-factory can leave a spec in when nothing will advance it:
// `draft` (never approved), `approved` (its prompt was never generated) and
// `prompted` (its prompt exists but does not link back). Enumerated rather than
// expressed as "anything not drained", because an unreadable or not-yet-written
// spec file reads as the empty status and must never be mistaken for a stall.
const (
	specStatusDraft    = "draft"
	specStatusApproved = "approved"
	specStatusPrompted = "prompted"
)

// isStallableSpecStatus reports whether status is one that can strand a run.
// `generating` is deliberately absent: dark-factory drives it and
// runner/lifecycle.go resumes or resets it after a restart, so aborting there
// would kill healthy work. `verifying` / `completed` are what drained() waits
// for, and the empty status is the not-yet-readable file.
func isStallableSpecStatus(status string) bool {
	switch status {
	case specStatusDraft, specStatusApproved, specStatusPrompted:
		return true
	}
	return false
}

// stallGracePolls is how many consecutive polls the stalled-spec condition must
// hold before waitForDrain aborts. Grace is required because a healthy run is
// momentarily in this state: the spec flips to `prompted` while its prompt sits
// in the inbox, and the sweeper promotes it to `verifying` only on a later tick.
//
// Ten polls (30s at the default interval) rather than three: the condition now
// also covers specs stranded at `draft` / `approved`, which have no prompt to
// watch, so the grace must outlast the daemon's own pickup tick as well as the
// `prompted` flicker. Still turns a 90-minute slot-hold into 30 seconds.
const stallGracePolls = 10

// darkFactoryRunner is the production ExecutionRunner: it shells `dark-factory`
// and `git` with the worktree as cwd, inheriting the pod env (HOME for claude
// auth, GH_TOKEN for push). It is exercised by the live smoke, not unit tests
// (the step's own tests fake this seam).
type darkFactoryRunner struct {
	binary       string
	pollInterval time.Duration
	timeout      time.Duration
}

// NewExecutionRunner constructs the production dark-factory/git lifecycle runner
// with the package-default lifecycleTimeout (90m), honoring a LIFECYCLE_TIMEOUT
// env override (Go duration, e.g. "90m", "2h") when set.
func NewExecutionRunner() ExecutionRunner {
	return NewExecutionRunnerWithTimeout(resolveLifecycleTimeoutFromEnv(lifecycleTimeout))
}

// resolveLifecycleTimeoutFromEnv returns the LIFECYCLE_TIMEOUT env value parsed
// as a Go duration, or fallback when unset/unparsable. Invalid values log a
// warning and fall back (a misconfigured timeout must not silently become zero).
func resolveLifecycleTimeoutFromEnv(fallback time.Duration) time.Duration {
	raw := os.Getenv("LIFECYCLE_TIMEOUT")
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		glog.Warningf("parsing LIFECYCLE_TIMEOUT=%q: %v — using default %s", raw, err, fallback)
		return fallback
	}
	if d <= 0 {
		glog.Warningf("non-positive LIFECYCLE_TIMEOUT=%q — using default %s", raw, fallback)
		return fallback
	}
	return d
}

// NewExecutionRunnerWithTimeout constructs the lifecycle runner with an explicit
// timeout. Used when the caller resolves a per-task `lifecycle_timeout_minutes:`
// frontmatter override (execution step) or a LIFECYCLE_TIMEOUT env override.
func NewExecutionRunnerWithTimeout(timeout time.Duration) ExecutionRunner {
	return &darkFactoryRunner{
		binary:       darkFactoryBinary,
		pollInterval: lifecyclePollInterval,
		timeout:      timeout,
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
// prompts remain in prompts/in-progress, or ctx (timeout) fires. A prompt whose
// frontmatter reaches `status: failed` fails the wait immediately: the daemon's
// failure handler marks a prompt failed only once retries are exhausted
// (re-queues re-mark it `approved`), the scanner then skips it forever ("queue
// blocked until manual retry"), so its spec can never reach verifying — without
// this fast-path the loop would spin until the 30-minute lifecycle deadline
// before escalating the exact same failure.
func (r *darkFactoryRunner) waitForDrain(
	ctx context.Context,
	workdir string,
	specIDs []string,
) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	stalledPolls := 0
	for {
		if file, reason := findFailedPrompt(ctx, workdir); file != "" {
			return errors.Errorf(ctx, "prompt %s failed permanently: %s", file, reason)
		}
		if r.drained(ctx, workdir, specIDs) {
			return nil
		}
		// A spec in a non-terminal status that no prompt references can never
		// advance, so waiting for it only holds the Job's slot until the
		// deadline. Fail on the first such spec after the grace window instead.
		if id := findStalledSpec(ctx, workdir, specIDs); id != "" {
			if stalledPolls++; stalledPolls >= stallGracePolls {
				return errors.Errorf(
					ctx,
					"lifecycle stalled: spec %s is %q with no prompt in flight "+
						"referencing it, so dark-factory can never advance it — "+
						"holding the slot longer cannot change the outcome",
					id, readSpecStatus(ctx, workdir, id),
				)
			}
		} else {
			stalledPolls = 0
		}
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx, ctx.Err(), "lifecycle did not drain before deadline")
		case <-ticker.C:
		}
	}
}

// findStalledSpec returns the id of a requested spec that is stranded in a
// status dark-factory cannot advance on its own, or "" when none is.
//
// `prompted` is the case that cost a full lifecycle deadline on 2026-10-20: a
// fallback path hand-authored a prompt with an empty `spec:` field, the
// implementation executed and committed, and the spec then sat at `prompted`
// forever because dark-factory's CheckAndComplete no-ops when no prompt carries
// the link (`found == false`). Nothing in the worktree changes again, so the
// run holds its only agent slot until the deadline and reports failure over work
// that actually succeeded.
//
// The same deadlock is not specific to `prompted`. drained() accepts only
// `verifying` / `completed`, so a spec left at `draft` or `approved` keeps
// drained() false forever too — and the original detector could not see either,
// because it compared against `prompted` alone. Measured 2026-10-22 17:28Z →
// 18:34Z: spec 063-a2a-default-cluster sat at `verifying` while the daemon
// logged "nothing to do, waiting for changes" 45 times over ~66 min, because a
// sibling spec in the same request never advanced and no prompt was in flight.
//
// `generating` is excluded and an unreadable status is excluded — see
// isStallableSpecStatus.
//
// Returns "" while work is in flight — a prompt that has just landed in the
// inbox is precisely the one about to be approved and executed.
func findStalledSpec(ctx context.Context, workdir string, specIDs []string) string {
	if hasInboxPrompts(workdir) || hasInProgressPrompts(workdir) {
		return ""
	}
	for _, id := range specIDs {
		if !isStallableSpecStatus(readSpecStatus(ctx, workdir, id)) {
			continue
		}
		if !anyPromptReferencesSpec(ctx, workdir, id) {
			return id
		}
	}
	return ""
}

// anyPromptReferencesSpec reports whether any prompt in the inbox, the queue or
// the completed dir carries specID in its `spec` frontmatter. Mirrors
// dark-factory's own matching: the field is normally a YAML list
// (`spec: ["003-foo"]`) but may be a bare scalar, and comparison is by numeric
// prefix so `003-json-readiness-endpoint`, `003` and `3` all match.
func anyPromptReferencesSpec(ctx context.Context, workdir, specID string) bool {
	want := specNumPrefix(specID)
	for _, dir := range []string{"", "in-progress", "completed"} {
		// A cancelled scan reports the spec as linked, never as stalled: the
		// caller is already unwinding on ctx.Done(), and an aborted read must
		// not be read as evidence that no prompt references the spec.
		if ctx.Err() != nil {
			return true
		}
		matches, _ := filepath.Glob(filepath.Join(workdir, "prompts", dir, "*.md"))
		if anyPromptInFilesReferencesSpec(ctx, matches, want, specID) {
			return true
		}
	}
	return false
}

// anyPromptInFilesReferencesSpec reports whether any of paths carries the spec
// link. Split from anyPromptReferencesSpec so each loop stays under the
// cognitive-complexity ceiling.
func anyPromptInFilesReferencesSpec(
	ctx context.Context,
	paths []string,
	want, specID string,
) bool {
	for _, path := range paths {
		if ctx.Err() != nil {
			return true
		}
		content, err := os.ReadFile(
			path,
		) // #nosec G304 -- path from Glob over the managed worktree
		if err != nil {
			continue
		}
		parsed, err := agentlib.ParseMarkdown(ctx, string(content))
		if err != nil {
			continue
		}
		for _, spec := range promptSpecValues(parsed.Frontmatter["spec"]) {
			if specNumPrefix(spec) == want || spec == specID {
				return true
			}
		}
	}
	return false
}

// promptSpecValues normalizes a prompt's `spec` frontmatter to a slice of
// strings, accepting both the list form dark-factory writes and a bare scalar.
func promptSpecValues(raw any) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

// specNumPrefix returns the numeric prefix of a spec id ("003-foo" -> "3"),
// or the whole value when it has none. Comparison is numeric so `003`, `3` and
// `003-json-readiness-endpoint` all identify the same spec.
func specNumPrefix(id string) string {
	id = strings.TrimSpace(id)
	end := 0
	for end < len(id) && id[end] >= '0' && id[end] <= '9' {
		end++
	}
	if end == 0 {
		return id
	}
	trimmed := strings.TrimLeft(id[:end], "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
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

// findFailedPrompt scans prompts/in-progress for a prompt whose frontmatter is
// `status: failed` — the daemon's terminal failure state (retry-eligible prompts
// are re-marked `approved`, so `failed` never means "will retry"). Returns the
// first failed prompt's basename and its lastFailReason ("" when none failed).
// Unreadable/unparseable files are skipped: transient (mid-write) states must
// not fail the lifecycle.
func findFailedPrompt(ctx context.Context, workdir string) (string, string) {
	matches, _ := filepath.Glob(filepath.Join(workdir, "prompts", "in-progress", "*.md"))
	for _, path := range matches {
		content, err := os.ReadFile(path) // #nosec G304 -- path from Glob over the managed worktree
		if err != nil {
			continue
		}
		parsed, err := agentlib.ParseMarkdown(ctx, string(content))
		if err != nil {
			continue
		}
		status, _ := parsed.Frontmatter.String("status")
		if strings.TrimSpace(status) != "failed" {
			continue
		}
		reason, _ := parsed.Frontmatter.String("lastFailReason")
		if reason = strings.TrimSpace(reason); reason == "" {
			reason = "no lastFailReason recorded"
		}
		return filepath.Base(path), reason
	}
	return "", ""
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
