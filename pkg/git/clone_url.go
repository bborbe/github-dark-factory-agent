// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
)

var cloneURLSegmentRegexp = regexp.MustCompile(`^[a-zA-Z0-9._\-]+$`)

// scpURLRegexp matches SCP-style SSH clone URLs like
// "git@github.com:owner/repo.git" — host and path separated by a single
// colon (NOT "://"), with a user@ prefix.
var scpURLRegexp = regexp.MustCompile(
	`^[a-zA-Z0-9._\-]+@([a-zA-Z0-9.\-]+):([^:].*)$`,
)

// CloneURLParts holds the validated components of a git clone URL.
type CloneURLParts struct {
	Host  string
	Owner string
	Repo  string
}

// ParseCloneURLParts parses a git clone URL into its host, owner, and repo
// components. Accepts URL-form ("https://host/owner/repo.git") and SCP-form
// SSH ("user@host:owner/repo.git"). Returns an error for malformed or unsafe
// inputs.
func ParseCloneURLParts(ctx context.Context, rawURL string) (*CloneURLParts, error) {
	if rawURL == "" {
		return nil, errors.Errorf(ctx, "clone URL must not be empty")
	}

	host, path, err := splitCloneURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")

	segments := strings.Split(path, "/")
	if len(segments) != 2 {
		return nil, errors.Errorf(
			ctx,
			"clone URL path must have exactly 2 segments (<owner>/<repo>), got %d: %s",
			len(segments),
			rawURL,
		)
	}

	for _, seg := range segments {
		if err := validateCloneURLSegment(ctx, seg); err != nil {
			return nil, err
		}
	}

	return &CloneURLParts{Host: host, Owner: segments[0], Repo: segments[1]}, nil
}

// ParseCloneURL converts a git clone URL to a relative bare-repo path:
// "<host>/<owner>/<repo>.git". Accepts URL-form ("https://host/owner/repo.git")
// and SCP-form SSH ("user@host:owner/repo.git"). Returns an error for malformed
// or unsafe inputs.
func ParseCloneURL(ctx context.Context, rawURL string) (string, error) {
	parts, err := ParseCloneURLParts(ctx, rawURL)
	if err != nil {
		return "", err
	}
	return parts.Host + "/" + parts.Owner + "/" + parts.Repo + ".git", nil
}

// splitCloneURL extracts (host, path) from either a standard URL or an
// SCP-style SSH form. Detects SCP-style first because url.Parse mishandles
// it (treats "git@host" as opaque scheme).
func splitCloneURL(ctx context.Context, rawURL string) (string, string, error) {
	if m := scpURLRegexp.FindStringSubmatch(rawURL); m != nil {
		return m[1], m[2], nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", errors.Wrapf(ctx, err, "parse clone URL")
	}
	if parsed.Host == "" {
		return "", "", errors.Errorf(ctx, "clone URL missing host: %s", rawURL)
	}
	return parsed.Host, parsed.Path, nil
}

func validateCloneURLSegment(ctx context.Context, seg string) error {
	if seg == "" {
		return errors.Errorf(ctx, "clone URL segment must not be empty")
	}
	if seg == "." || seg == ".." {
		return errors.Errorf(ctx, "clone URL segment must not be '.' or '..': %s", seg)
	}
	if !cloneURLSegmentRegexp.MatchString(seg) {
		return errors.Errorf(ctx, "clone URL segment contains invalid characters: %s", seg)
	}
	return nil
}
