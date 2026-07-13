// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import (
	"regexp"
	"strings"
)

var branchNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9._/@\-]+$`)

// isValidBranchName returns true if b is a safe, well-formed git branch name.
// Rejects empty strings, names starting with "-", and names containing "..".
func isValidBranchName(b string) bool {
	if b == "" {
		return false
	}
	if strings.HasPrefix(b, "-") {
		return false
	}
	if strings.Contains(b, "..") {
		return false
	}
	return branchNameRegexp.MatchString(b)
}
