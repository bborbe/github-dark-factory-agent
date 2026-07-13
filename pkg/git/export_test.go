// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

// CmdEnv exposes the package-private allowlist-env helper for direct testing
// without exec'ing real git subprocesses. Production callers must not depend on
// this — use NewRepoManager and run an actual git operation instead.
func CmdEnv(m RepoManager) []string {
	r, ok := m.(*repoManager)
	if !ok {
		return nil
	}
	return r.cmdEnv()
}

// IsValidBranchName exposes the package-private branch-name validator for
// direct unit testing.
func IsValidBranchName(b string) bool {
	return isValidBranchName(b)
}
