// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg/git"
)

var _ = Describe("RepoManager", func() {
	var (
		ctx      context.Context
		reposDir string
		workDir  string
		manager  git.RepoManager
		origin   *testOrigin
	)

	BeforeEach(func() {
		ctx = context.Background()
		reposDir = GinkgoT().TempDir()
		workDir = GinkgoT().TempDir()
		manager = git.NewRepoManager(git.WorkdirConfig{
			ReposPath: reposDir,
			WorkPath:  workDir,
		}, "")
		origin = newTestOrigin()
		DeferCleanup(os.RemoveAll, origin.Path)
	})

	Describe("EnsureBareClone", func() {
		It("clones bare repo and returns a valid path", func() {
			barePath, err := manager.EnsureBareClone(ctx, origin.URL)
			Expect(err).To(BeNil())
			Expect(barePath).NotTo(BeEmpty())
			out, gitErr := exec.Command("git", "-C", barePath, "rev-parse", "--git-dir").Output()
			Expect(gitErr).To(BeNil())
			Expect(strings.TrimSpace(string(out))).To(Equal("."))
		})

		It("fetches without re-cloning and returns the same path", func() {
			barePath1, err := manager.EnsureBareClone(ctx, origin.URL)
			Expect(err).To(BeNil())
			barePath2, err := manager.EnsureBareClone(ctx, origin.URL)
			Expect(err).To(BeNil())
			Expect(barePath2).To(Equal(barePath1))
		})

		It("removes a half-clone directory and re-clones", func() {
			relPath, parseErr := git.ParseCloneURL(ctx, origin.URL)
			Expect(parseErr).To(BeNil())
			barePath := filepath.Join(reposDir, relPath)
			Expect(os.MkdirAll(barePath, 0750)).To(BeNil())
			Expect(
				os.WriteFile(filepath.Join(barePath, "garbage.txt"), []byte("junk"), 0600),
			).To(BeNil())

			result, err := manager.EnsureBareClone(ctx, origin.URL)
			Expect(err).To(BeNil())
			Expect(result).To(Equal(barePath))
			out, gitErr := exec.Command("git", "-C", barePath, "rev-parse", "--git-dir").Output()
			Expect(gitErr).To(BeNil())
			Expect(strings.TrimSpace(string(out))).To(Equal("."))
		})

		It("returns a descriptive error for an invalid clone URL", func() {
			_, err := manager.EnsureBareClone(ctx, "not-a-valid-url")
			Expect(err).NotTo(BeNil())
		})
	})

	Describe("EnsureWorktree", func() {
		const taskID = "bd4d883b-1234-5678-abcd-123456789012"

		It("creates a worktree at the branch ref", func() {
			wPath, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
			Expect(err).To(BeNil())
			Expect(wPath).To(Equal(filepath.Join(workDir, taskID)))
			out, gitErr := exec.Command("git", "-C", wPath, "rev-parse", "--abbrev-ref", "HEAD").
				Output()
			Expect(gitErr).To(BeNil())
			Expect(strings.TrimSpace(string(out))).To(Equal("feature-branch"))
		})

		It("returns the same path on the second call (idempotent)", func() {
			wPath1, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
			Expect(err).To(BeNil())
			wPath2, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
			Expect(err).To(BeNil())
			Expect(wPath2).To(Equal(wPath1))
		})

		It("rejects an invalid taskID before touching disk", func() {
			_, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", "not-a-uuid")
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("invalid task ID"))
			_, statErr := os.Stat(filepath.Join(workDir, "not-a-uuid"))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})

		It("rejects an invalid ref before touching disk", func() {
			_, err := manager.EnsureWorktree(ctx, origin.URL, "-bad-ref", taskID)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("invalid ref"))
		})
	})

	Describe("PruneAllWorktrees", func() {
		It("is a no-op and returns nil when reposPath does not exist", func() {
			m := git.NewRepoManager(git.WorkdirConfig{
				ReposPath: filepath.Join(GinkgoT().TempDir(), "nonexistent"),
				WorkPath:  workDir,
			}, "")
			Expect(m.PruneAllWorktrees(ctx)).To(BeNil())
		})

		It("prunes without error against a real bare clone", func() {
			_, err := manager.EnsureBareClone(ctx, origin.URL)
			Expect(err).To(BeNil())
			Expect(manager.PruneAllWorktrees(ctx)).To(BeNil())
		})
	})

	Describe("cmdEnv", func() {
		proxyKeys := []string{
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
			"http_proxy", "https_proxy", "no_proxy",
		}

		// Clear any proxy variables inherited from the test runner's own
		// environment; specs in one process share the environment, so each spec
		// opts in to exactly the proxy values it wants to observe.
		BeforeEach(func() {
			for _, key := range proxyKeys {
				GinkgoT().Setenv(key, "")
			}
		})

		It("returns nil when no token is configured", func() {
			m := git.NewRepoManager(git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir}, "")
			Expect(git.CmdEnv(m)).To(BeNil())
		})

		It("prefers the live GH_TOKEN over the one captured at construction", func() {
			// Installation tokens expire after 60 minutes, and the execution phase
			// re-mints immediately before its push. A value captured at
			// construction would go stale, and every later git call in the same
			// run — headAfterPush, ai_review's fetch — would authenticate with the
			// expired token.
			GinkgoT().Setenv("GH_TOKEN", "ghs-LIVE-NOT-REAL")

			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				"ghs-CAPTURED-NOT-REAL",
			)
			env := git.CmdEnv(m)
			Expect(env).To(ContainElement("GH_TOKEN=ghs-LIVE-NOT-REAL"))
			Expect(env).NotTo(ContainElement("GH_TOKEN=ghs-CAPTURED-NOT-REAL"))
		})

		It(
			"returns an allowlist env with GH_TOKEN, HOME and PATH when a token is configured",
			func() {
				const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
				m := git.NewRepoManager(
					git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
					token,
				)
				env := git.CmdEnv(m)
				Expect(env).To(ContainElement("GH_TOKEN=" + token))
				Expect(env).To(HaveLen(3))
				homeFound, pathFound := false, false
				for _, e := range env {
					if strings.HasPrefix(e, "HOME=") {
						homeFound = true
					}
					if strings.HasPrefix(e, "PATH=") {
						pathFound = true
					}
				}
				Expect(homeFound).To(BeTrue())
				Expect(pathFound).To(BeTrue())
			},
		)

		It("passes through proxy variables when a token is configured", func() {
			// A sandbox that admits egress only through a proxy refuses a direct
			// connection in milliseconds. The git child must see the proxy just
			// like the agent process does, or every clone fails at 2-9 ms.
			GinkgoT().Setenv("HTTPS_PROXY", "http://proxy.internal:3128")
			GinkgoT().Setenv("https_proxy", "http://proxy.internal:3128")

			const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				token,
			)
			env := git.CmdEnv(m)

			Expect(env).To(ContainElement("HTTPS_PROXY=http://proxy.internal:3128"))
			Expect(env).To(ContainElement("https_proxy=http://proxy.internal:3128"))
			Expect(env).To(ContainElement("GH_TOKEN=" + token))
		})

		It("omits unset proxy variables entirely — no empty KEY= entries", func() {
			const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				token,
			)
			env := git.CmdEnv(m)

			for _, e := range env {
				for _, key := range proxyKeys {
					Expect(e).NotTo(HavePrefix(key+"="),
						"unset proxy %s must produce no entry at all", key)
				}
			}
			Expect(env).To(HaveLen(3))
		})

		It("actually delivers the proxy to a child process", func() {
			// The slice assertions above are necessary but not sufficient: this
			// spec proves the child inherits the value through exec.Cmd.Env.
			GinkgoT().Setenv("HTTPS_PROXY", "http://proxy.internal:3128")

			const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				token,
			)

			cmd := exec.CommandContext(ctx, "sh", "-c", `echo "$HTTPS_PROXY"`)
			cmd.Env = git.CmdEnv(m)
			out, err := cmd.Output()
			Expect(err).To(BeNil())
			Expect(strings.TrimSpace(string(out))).To(Equal("http://proxy.internal:3128"))
		})

		It("passes through every one of the six proxy variables when set", func() {
			// libcurl (git over https) reads the lowercase forms and ignores an
			// uppercase HTTP_PROXY, while other clients read the uppercase forms,
			// so dropping either spelling breaks a real client.
			for i, key := range proxyKeys {
				GinkgoT().Setenv(key, fmt.Sprintf("value-%d", i))
			}

			const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				token,
			)
			env := git.CmdEnv(m)

			for i, key := range proxyKeys {
				Expect(env).To(ContainElement(fmt.Sprintf("%s=value-%d", key, i)))
			}
			Expect(env).To(ContainElement("GH_TOKEN=" + token))
			Expect(env).To(HaveLen(3 + len(proxyKeys)))
		})

		It("excludes an unrelated secret-shaped variable while proxies are set", func() {
			// The allowlist exists so pod-level secrets never reach git or a
			// helper it shells out to; admitting the proxy must not turn it into
			// a pass-through.
			GinkgoT().Setenv("SOME_TOKEN", "must-not-leak")
			GinkgoT().Setenv("HTTPS_PROXY", "http://proxy.internal:3128")

			const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				token,
			)
			env := git.CmdEnv(m)

			for _, e := range env {
				Expect(e).NotTo(HavePrefix("SOME_TOKEN="))
				Expect(e).NotTo(ContainSubstring("must-not-leak"))
			}
			Expect(env).To(ContainElement("GH_TOKEN=" + token))
			Expect(env).To(ContainElement("HTTPS_PROXY=http://proxy.internal:3128"))
		})
	})
})

// testOrigin holds a temporary non-bare git repository used as the remote in
// RepoManager tests.
type testOrigin struct {
	Path      string
	URL       string
	CommitSHA string
}

// newTestOrigin creates a temporary git repository that RepoManager can clone
// from. The repo is placed under /tmp explicitly (NOT os.TempDir(), which
// resolves to a multi-component path on macOS) so the file://localhost URL
// yields a 2-segment path ParseCloneURL accepts.
func newTestOrigin() *testOrigin {
	dir, err := os.MkdirTemp("/tmp", "gittest")
	Expect(err).To(BeNil())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })

	runCmd(dir, "git", "init")
	runCmd(dir, "git", "config", "user.email", "test@example.com")
	runCmd(dir, "git", "config", "user.name", "Test User")

	Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0600)).To(BeNil())
	runCmd(dir, "git", "add", "README.md")
	runCmd(dir, "git", "commit", "-m", "Initial commit")

	runCmd(dir, "git", "checkout", "-b", "feature-branch")
	Expect(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("Feature\n"), 0600)).To(BeNil())
	runCmd(dir, "git", "add", "feature.txt")
	runCmd(dir, "git", "commit", "-m", "Add feature")

	shaBytes, shaErr := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	Expect(shaErr).To(BeNil())
	commitSHA := strings.TrimSpace(string(shaBytes))

	components := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	Expect(len(components)).To(Equal(2),
		"test origin path must have exactly 2 path components; got %d (%s)", len(components), dir,
	)

	return &testOrigin{
		Path:      dir,
		URL:       "file://localhost/" + strings.Join(components, "/"),
		CommitSHA: commitSHA,
	}
}

func runCmd(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	Expect(err).To(BeNil(), "%s %v failed: %s", name, args, string(out))
}
