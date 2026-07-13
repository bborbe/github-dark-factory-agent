// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

// WorkdirConfig holds the root paths for bare-clone caching and per-task worktrees.
type WorkdirConfig struct {
	ReposPath string // root for bare clones: <ReposPath>/<host>/<owner>/<repo>.git
	WorkPath  string // root for worktrees:   <WorkPath>/<task_id>
}
