// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// RepoManager manages bare clone caching and per-task worktrees.
//
//counterfeiter:generate -o ../../mocks/repo-manager.go --fake-name RepoManager . RepoManager
type RepoManager interface {
	// EnsureBareClone ensures a bare clone of cloneURL exists and is up to date.
	// Returns the absolute path to the bare repo.
	EnsureBareClone(ctx context.Context, cloneURL string) (string, error)
	// EnsureWorktree ensures a worktree for the given ref and taskID exists.
	// Returns the absolute path to the worktree.
	EnsureWorktree(ctx context.Context, cloneURL, ref, taskID string) (string, error)
	// PruneAllWorktrees runs `git worktree prune` on every bare repo under reposPath.
	PruneAllWorktrees(ctx context.Context) error
}

// NewRepoManager creates a RepoManager backed by the given WorkdirConfig.
//
// ghToken is exported as GH_TOKEN in every git subprocess env so the credential
// helper installed by `gh auth setup-git` (which shells out to `gh auth
// git-credential`) can authenticate HTTPS clones / fetches against private
// repos. Without this, git inherits the pod env (no GH_TOKEN) → `gh auth
// git-credential` returns nothing → clone fails with "authentication required"
// even though the token was minted successfully at startup.
//
// When ghToken is empty, git subprocesses inherit the parent env unchanged so
// the local-CLI / ambient-gh path (using the operator's own gh state) keeps
// working.
func NewRepoManager(cfg WorkdirConfig, ghToken string) RepoManager {
	return &repoManager{cfg: cfg, ghToken: ghToken}
}

type repoManager struct {
	cfg     WorkdirConfig
	ghToken string
}

// cmdEnv returns the allowlisted env for git subprocesses: HOME (for
// ~/.gitconfig), PATH (to resolve git + gh), GH_TOKEN (for the gh credential
// helper). Prevents unrelated pod secrets (DATABASE_URL, ANTHROPIC_AUTH_TOKEN,
// etc.) from leaking into git or any helper it shells out to.
//
// When ghToken is empty the parent env is returned unchanged so ambient paths
// (cmd/run-task with operator's own gh state) keep working.
func (r *repoManager) cmdEnv() []string {
	if r.ghToken == "" {
		return nil
	}
	return []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"GH_TOKEN=" + r.ghToken,
	}
}

var taskIDRegexp = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

func (r *repoManager) EnsureBareClone(ctx context.Context, cloneURL string) (string, error) {
	relPath, err := ParseCloneURL(ctx, cloneURL)
	if err != nil {
		return "", errors.Wrap(ctx, err, "parse clone URL failed")
	}

	barePath := filepath.Join(r.cfg.ReposPath, relPath)

	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		return r.cloneBare(ctx, cloneURL, barePath)
	}

	// Check if the existing directory is a valid bare repo.
	if err := r.runGitCmd(ctx, barePath, "rev-parse", "--git-dir"); err != nil {
		// Half-clone: remove and re-clone.
		if removeErr := os.RemoveAll(barePath); removeErr != nil {
			return "", errors.Wrapf(ctx, removeErr, "remove half-clone %s", barePath)
		}
		return r.cloneBare(ctx, cloneURL, barePath)
	}

	// Valid bare repo: refresh remote-tracking refs (origin/*) so the daemon's
	// per-prompt `git merge origin/<default>` resolves.
	if err := r.configureRemoteTracking(ctx, barePath); err != nil {
		return "", err
	}

	return barePath, nil
}

func (r *repoManager) cloneBare(ctx context.Context, cloneURL, barePath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(barePath), 0750); err != nil {
		return "", errors.Wrapf(ctx, err, "create parent dir for %s", barePath)
	}

	// #nosec G204 -- cloneURL is validated by ParseCloneURL; barePath is constructed from validated components
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", cloneURL, barePath)
	cmd.Env = r.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.Errorf(ctx, "git clone --bare: %s", strings.TrimSpace(stderr.String()))
	}

	if err := r.configureRemoteTracking(ctx, barePath); err != nil {
		return "", err
	}

	return barePath, nil
}

// configureRemoteTracking makes `origin/<branch>` (refs/remotes/origin/*)
// resolvable in worktrees of this bare repo, then fetches. A `git clone --bare`
// sets the fetch refspec to +refs/heads/*:refs/heads/* (no remote-tracking
// refs), but dark-factory's per-prompt "sync with default branch" (run by every
// workflow) does `git merge origin/<default>` in the worktree; without
// refs/remotes/origin/* that merge fails with "origin/master - not something we
// can merge". Setting the standard normal-clone refspec + fetching populates
// origin/* in the shared object store the worktrees see.
func (r *repoManager) configureRemoteTracking(ctx context.Context, barePath string) error {
	if err := r.runGitCmd(
		ctx, barePath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*",
	); err != nil {
		return errors.Wrap(ctx, err, "configure origin fetch refspec")
	}
	if err := r.runGitCmd(ctx, barePath, "fetch", "--prune", "origin"); err != nil {
		return errors.Wrap(ctx, err, "git fetch remote-tracking refs")
	}
	return nil
}

func (r *repoManager) EnsureWorktree(
	ctx context.Context,
	cloneURL, ref, taskID string,
) (string, error) {
	if !isValidBranchName(ref) {
		return "", errors.Errorf(ctx, "invalid ref: %s", ref)
	}

	if !taskIDRegexp.MatchString(taskID) {
		return "", errors.Errorf(ctx, "invalid task ID (must be UUID): %s", taskID)
	}

	barePath, err := r.EnsureBareClone(ctx, cloneURL)
	if err != nil {
		return "", errors.Wrap(ctx, err, "ensure bare clone failed")
	}

	worktreePath := filepath.Join(r.cfg.WorkPath, taskID)

	if _, err := os.Stat(worktreePath); err == nil {
		return worktreePath, nil
	}

	// #nosec G204 -- barePath constructed from validated URL; worktreePath joined from UUID-validated taskID; ref validated by isValidBranchName
	cmd := exec.CommandContext(ctx, "git", "-C", barePath, "worktree", "add", worktreePath, ref)
	cmd.Env = r.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.Errorf(ctx, "git worktree add: %s", strings.TrimSpace(stderr.String()))
	}

	return worktreePath, nil
}

func (r *repoManager) PruneAllWorktrees(ctx context.Context) error {
	if _, err := os.Stat(r.cfg.ReposPath); os.IsNotExist(err) {
		return nil
	}

	return filepath.WalkDir(r.cfg.ReposPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || !strings.HasSuffix(d.Name(), ".git") {
			return nil
		}

		// #nosec G204 -- path comes from WalkDir over r.cfg.ReposPath (operator-controlled); name ends in ".git" (validated above)
		cmd := exec.CommandContext(ctx, "git", "-C", path, "worktree", "prune")
		cmd.Env = r.cmdEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if runErr := cmd.Run(); runErr != nil {
			glog.Warningf(
				"git worktree prune in %s failed: %s",
				path,
				strings.TrimSpace(stderr.String()),
			)
		}

		return filepath.SkipDir
	})
}

func (r *repoManager) runGitCmd(ctx context.Context, repoPath string, args ...string) error {
	// #nosec G204 -- repoPath is from validated bare clone path; args are hardcoded git subcommands
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	cmd.Env = r.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(ctx, "git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return nil
}
