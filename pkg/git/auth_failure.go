// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import "strings"

// gitAuthFailureSubstrings are the error message fragments that git (and GitHub)
// produce when authentication fails on an HTTPS clone. The agent detects these
// to distinguish "no usable credentials" from generic network or config errors.
//
// TRADE-OFF: substring matching is pattern-based and brittle — git or gh CLI
// upgrades may rephrase these strings, silently breaking detection.
//
// "Repository not found" is intentionally classified as auth failure: GitHub
// returns this exact message when an unauthenticated client requests a private
// repository.
var gitAuthFailureSubstrings = []string{
	"could not read Username",
	"Authentication failed",
	"Repository not found",
	"returned error: 403",
	"returned error: 401",
}

// IsGitAuthFailure reports whether err looks like a git authentication failure
// on an HTTPS remote. Returns false for nil.
func IsGitAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, sub := range gitAuthFailureSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}
