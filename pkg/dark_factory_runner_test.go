// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-dark-factory-agent/pkg"
)

// writeStub writes an executable /bin/sh stub and returns its path.
func writeStub(dir, body string) string {
	path := filepath.Join(dir, "df-stub.sh")
	Expect(os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700)).To(BeNil()) // #nosec G306
	return path
}

// gitInit runs git in dir, failing the spec on error.
func gitInit(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	Expect(err).To(BeNil(), string(out))
}

var _ = Describe("darkFactoryRunner", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	Describe("filesystem helpers", func() {
		var work string
		BeforeEach(func() {
			work = GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(work, "specs", "in-progress"), 0750)).To(BeNil())
			Expect(os.MkdirAll(filepath.Join(work, "specs", "completed"), 0750)).To(BeNil())
			Expect(os.MkdirAll(filepath.Join(work, "prompts", "in-progress"), 0750)).To(BeNil())
			Expect(os.MkdirAll(filepath.Join(work, "prompts", "completed"), 0750)).To(BeNil())
		})

		It("reads spec status from in-progress and completed", func() {
			Expect(os.WriteFile(
				filepath.Join(work, "specs", "in-progress", "001-hello.md"),
				[]byte("---\nstatus: verifying\n---\n\nbody\n"), 0600,
			)).To(BeNil())
			Expect(pkg.ReadSpecStatus(ctx, work, "001-hello")).To(Equal("verifying"))

			Expect(os.WriteFile(
				filepath.Join(work, "specs", "completed", "002-done.md"),
				[]byte("---\nstatus: completed\n---\n\nbody\n"), 0600,
			)).To(BeNil())
			Expect(pkg.ReadSpecStatus(ctx, work, "002-done")).To(Equal("completed"))
		})

		It("returns empty status for an absent spec", func() {
			Expect(pkg.ReadSpecStatus(ctx, work, "999-missing")).To(Equal(""))
		})

		It("detects in-progress prompts and counts completed prompts", func() {
			Expect(pkg.HasInProgressPrompts(work)).To(BeFalse())
			Expect(pkg.CountCompletedPrompts(work)).To(Equal(0))

			Expect(os.WriteFile(
				filepath.Join(work, "prompts", "in-progress", "p1.md"), []byte("x"), 0600,
			)).To(BeNil())
			Expect(pkg.HasInProgressPrompts(work)).To(BeTrue())

			Expect(os.WriteFile(
				filepath.Join(work, "prompts", "completed", "p1.md"), []byte("x"), 0600,
			)).To(BeNil())
			Expect(pkg.CountCompletedPrompts(work)).To(Equal(1))
		})

		It("detects inbox prompts (prompts/*.md at root) but not the subdirectories", func() {
			// A completed prompt in prompts/completed must NOT register as inbox.
			Expect(os.WriteFile(
				filepath.Join(work, "prompts", "completed", "done.md"), []byte("x"), 0600,
			)).To(BeNil())
			Expect(pkg.HasInboxPrompts(work)).To(BeFalse())

			// A generated prompt awaiting auto-approve lands in the inbox (root).
			Expect(os.WriteFile(
				filepath.Join(work, "prompts", "001-inbox.md"), []byte("x"), 0600,
			)).To(BeNil())
			Expect(pkg.HasInboxPrompts(work)).To(BeTrue())
		})
	})

	Describe("RunLifecycle (stub daemon)", func() {
		It("starts the daemon, waits for drain, and reports spec status + prompt count", func() {
			work := GinkgoT().TempDir()
			// Stub daemon: create a completed prompt + a verifying spec, then idle
			// until the process group is killed by stopDaemon.
			stub := writeStub(work, `
case "$1" in
  daemon)
    mkdir -p prompts/completed specs/in-progress
    printf 'x' > prompts/completed/001-p.md
    printf -- '---\nstatus: verifying\n---\n\nbody\n' > specs/in-progress/001-hello.md
    while true; do sleep 0.1; done ;;
  *) exit 0 ;;
esac
`)
			runner := pkg.NewTestExecutionRunner(stub, 20*time.Millisecond, 10*time.Second)
			res, err := runner.RunLifecycle(
				ctx,
				work,
				[]string{"001-hello"},
				[]string{"--set", "backend=local"},
			)
			Expect(err).To(BeNil())
			Expect(res.PromptsExecuted).To(Equal(1))
			Expect(res.SpecStatuses["001-hello"]).To(Equal("verifying"))
		})

		It(
			"does NOT drain while a generated prompt is still in the inbox (auto-approve in flight)",
			func() {
				work := GinkgoT().TempDir()
				// Reproduce the auto-approve window: the daemon has flipped the spec to
				// `verifying` but the generated prompt is still in the INBOX
				// (prompts/*.md at root), awaiting the audit that moves it to
				// prompts/in-progress. drained() must treat the inbox as work-in-flight;
				// otherwise stopDaemon SIGKILLs the in-flight audit and the marker is
				// never created. With the fix the lifecycle never drains → deadline error.
				stub := writeStub(work, `
case "$1" in
  daemon)
    mkdir -p prompts specs/in-progress
    printf 'x' > prompts/001-inbox.md
    printf -- '---\nstatus: verifying\n---\n\nbody\n' > specs/in-progress/001-hello.md
    while true; do sleep 0.1; done ;;
  *) exit 0 ;;
esac
`)
				runner := pkg.NewTestExecutionRunner(
					stub,
					20*time.Millisecond,
					250*time.Millisecond,
				)
				_, err := runner.RunLifecycle(ctx, work, []string{"001-hello"}, nil)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("drain"))
			},
		)

		It("errors when the lifecycle does not drain before the deadline", func() {
			work := GinkgoT().TempDir()
			// Stub daemon that never produces the spec → never drains.
			stub := writeStub(
				work,
				"case \"$1\" in daemon) while true; do sleep 0.1; done ;; *) exit 0 ;; esac\n",
			)
			runner := pkg.NewTestExecutionRunner(stub, 20*time.Millisecond, 250*time.Millisecond)
			_, err := runner.RunLifecycle(ctx, work, []string{"001-hello"}, nil)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("drain"))
		})
	})

	Describe("CompleteSpec (stub binary)", func() {
		It("succeeds when the binary exits 0", func() {
			work := GinkgoT().TempDir()
			stub := writeStub(work, "exit 0\n")
			runner := pkg.NewTestExecutionRunner(stub, time.Millisecond, time.Second)
			Expect(runner.CompleteSpec(ctx, work, "001-hello")).To(BeNil())
		})

		It("errors when the binary exits non-zero", func() {
			work := GinkgoT().TempDir()
			stub := writeStub(work, "echo boom >&2; exit 1\n")
			runner := pkg.NewTestExecutionRunner(stub, time.Millisecond, time.Second)
			err := runner.CompleteSpec(ctx, work, "001-hello")
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("spec complete"))
		})
	})

	Describe("PushBranch (real git)", func() {
		var work string
		BeforeEach(func() {
			root := GinkgoT().TempDir()
			bare := filepath.Join(root, "remote.git")
			gitInit(root, "init", "--bare", bare)
			work = filepath.Join(root, "proj")
			Expect(os.MkdirAll(work, 0750)).To(BeNil())
			gitInit(work, "init", "-q", "-b", "master")
			gitInit(work, "config", "user.email", "t@t")
			gitInit(work, "config", "user.name", "t")
			Expect(os.WriteFile(filepath.Join(work, "f.txt"), []byte("x"), 0600)).To(BeNil())
			gitInit(work, "add", "-A")
			gitInit(work, "commit", "-qm", "init")
			gitInit(work, "remote", "add", "origin", bare)
		})

		It("pushes HEAD to origin/<branch>", func() {
			runner := pkg.NewTestExecutionRunner("dark-factory", time.Millisecond, time.Second)
			Expect(runner.PushBranch(ctx, work, "master")).To(BeNil())
		})

		It("errors when there is no origin remote", func() {
			gitInit(work, "remote", "remove", "origin")
			runner := pkg.NewTestExecutionRunner("dark-factory", time.Millisecond, time.Second)
			err := runner.PushBranch(ctx, work, "master")
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("git push"))
		})
	})
})
