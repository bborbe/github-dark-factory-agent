// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubauth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// tokenFileName is the file the file-backed credential helper reads at every
// credential lookup.
const tokenFileName = "token"

// TokenFilePath returns the absolute path of the token file the credential
// helper reads. It lives under the pod's HOME so a pod restart starts clean.
func TokenFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/home/node"
	}
	return filepath.Join(home, ".config", "gh-token", tokenFileName)
}

// credentialHelperCommand renders the git credential-helper shell snippet.
//
// git invokes a credential helper with `get` on stdin and reads `username=` /
// `password=` from stdout, at EVERY credential lookup — which is the whole
// point: a daemon started before a token refresh re-reads this file on its next
// `git fetch`, whereas an environment variable was copied at exec time and
// cannot be updated afterwards.
func credentialHelperCommand(tokenPath string) string {
	// Single-quote the path: HOME can contain spaces or shell metacharacters on
	// the local cmd/run-task path, and an unquoted interpolation would either
	// break the helper or be interpreted by the shell.
	quoted := "'" + strings.ReplaceAll(tokenPath, "'", `'\''`) + "'"
	return fmt.Sprintf(
		`!f() { case "$1" in get) echo username=x-access-token; echo "password=$(cat %s)";; esac; }; f`,
		quoted,
	)
}

// WriteTokenFile writes token to the helper's file with 0600 permissions,
// creating the parent directory with 0700. It is safe to call repeatedly; each
// call atomically replaces the contents.
func WriteTokenFile(ctx context.Context, token string) error {
	if token == "" {
		return errors.Errorf(ctx, "refusing to write an empty token file")
	}
	path := TokenFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.Wrap(ctx, err, "create token dir")
	}
	tmp := path + ".tmp"
	// O_CREATE|O_TRUNC with an explicit chmod: os.WriteFile only applies 0600 on
	// create, so a pre-existing token.tmp with wider permissions would be
	// truncated and renamed into place still carrying them.
	f, err := os.OpenFile(
		tmp,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0600,
	) // #nosec G304 -- fixed path
	if err != nil {
		return errors.Wrap(ctx, err, "open token file")
	}
	if _, err := f.WriteString(token); err != nil {
		_ = f.Close()
		return errors.Wrap(ctx, err, "write token file")
	}
	if err := f.Close(); err != nil {
		return errors.Wrap(ctx, err, "close token file")
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return errors.Wrap(ctx, err, "chmod token file")
	}
	// Rename so a concurrent reader never observes a half-written token.
	if err := os.Rename(tmp, path); err != nil {
		return errors.Wrap(ctx, err, "rename token file")
	}
	return nil
}

// fileBackedHelper installs a git credential helper that reads the token file.
type fileBackedHelper struct {
	token     string
	tokenPath string
	execFunc  func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NewFileBackedHelper returns a Configurator that writes token to the helper's
// file and points git's github.com credential helper at it.
//
// It exists because `gh auth setup-git` binds the token the process had at
// startup: gh's helper resolves GH_TOKEN from the environment of whatever runs
// git, and the dark-factory daemon captured that environment when it was
// started. The installation token lives 60 minutes and a lifecycle run routinely
// outlasts it, so a long run failed its own `git fetch` with "Invalid username
// or token" (2026-11-03, bborbe/trading#433: minted 18:10Z, fetch refused
// 19:15Z). A file the daemon re-reads has no such lifetime.
func NewFileBackedHelper(token string) Configurator {
	return &fileBackedHelper{
		token:     token,
		tokenPath: TokenFilePath(),
		execFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return defaultExecFunc(ctx, token, name, args...)
		},
	}
}

// NewFileBackedHelperWithExec is the injectable form used by tests.
func NewFileBackedHelperWithExec(
	token string,
	execFunc func(ctx context.Context, name string, args ...string) ([]byte, error),
) Configurator {
	return &fileBackedHelper{
		token:     token,
		tokenPath: TokenFilePath(),
		execFunc:  execFunc,
	}
}

// Setup writes the token file and installs the credential helper, replacing any
// helper gh installed so git does not fall back to a stale token first.
func (f *fileBackedHelper) Setup(ctx context.Context) error {
	if f.token == "" {
		glog.V(2).Infof("github-auth-setup: empty token, skipping file-backed helper")
		return nil
	}
	if err := WriteTokenFile(ctx, f.token); err != nil {
		return errors.Wrap(ctx, err, "write token file")
	}
	if _, err := f.execFunc(
		ctx,
		"git",
		"config",
		"--global",
		"--replace-all",
		"credential.https://github.com.helper",
		credentialHelperCommand(f.tokenPath),
	); err != nil {
		return errors.Wrap(ctx, err, "configure file-backed credential helper")
	}
	glog.V(2).Infof("github-auth-setup: file-backed credential helper installed at %s", f.tokenPath)
	return nil
}
