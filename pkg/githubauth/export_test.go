// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubauth

// DefaultExecFunc exposes the production exec wrapper for testing.
var DefaultExecFunc = defaultExecFunc

// NewGhAuthSetupGitWithExecFunc exposes the test-only constructor for
// injecting a fake exec function. Keeping the underlying function unexported
// in production prevents real callers from bypassing the production exec
// wrapper (which is the only path that goes through `cmd.CombinedOutput`).
var NewGhAuthSetupGitWithExecFunc = newGhAuthSetupGitWithExecFunc
