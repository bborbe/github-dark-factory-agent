// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
)

// apiBaseURL is the GitHub REST API root. Overridable in tests via
// newGitHubClient (export_test.go) so probes hit httptest instead.
const apiBaseURL = "https://api.github.com"

// PullRequestInfo is the subset of the GitHub pull-request payload the
// planning step needs: whether the PR is a draft and the SHA at its head.
type PullRequestInfo struct {
	Draft   bool
	HeadSHA string
}

// GitHubClient reads pull-request metadata from the GitHub REST API. Only the
// read paths the planning-phase preconditions require are exposed.
//
//counterfeiter:generate -o ../mocks/github-client.go --fake-name GitHubClient . GitHubClient
type GitHubClient interface {
	// GetPullRequest returns the draft flag and head SHA for a PR.
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*PullRequestInfo, error)
	// ListPullRequestFiles returns the repo-relative paths changed in a PR.
	ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]string, error)
}

// NewGitHubClient constructs a GitHubClient backed by the public GitHub REST
// API. token authenticates the requests; empty means anonymous (fine for
// public repos and for tests that stub the transport).
func NewGitHubClient(token string) GitHubClient {
	return newGitHubClient(token, apiBaseURL)
}

func newGitHubClient(token, baseURL string) *httpGitHubClient {
	return &httpGitHubClient{
		token:   token,
		baseURL: baseURL,
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: retryTransportFor(baseURL),
		},
	}
}

// retryTransportFor wraps the default transport with the shared rate-limit
// retry round tripper. api.github.com is always retried; the host of apiBase
// is added too when it differs, so a test seam pointing at a stub server is
// covered. MaxWait stays well below the client timeouts: a longer server
// requested wait is deferred, so the caller sees the real 403/429 instead of
// a context-deadline error when the client timeout fires mid-sleep.
func retryTransportFor(apiBase string) http.RoundTripper {
	hosts := []string{"api.github.com"}
	if u, err := url.Parse(apiBase); err == nil && u.Hostname() != "" &&
		u.Hostname() != "api.github.com" {
		hosts = append(hosts, u.Hostname())
	}
	return libhttp.NewRoundTripperRetryWithBackoff(
		http.DefaultTransport,
		libhttp.RetryBackoffOptions{RetryHosts: hosts, MaxWait: 5 * time.Second},
	)
}

type httpGitHubClient struct {
	token   string
	baseURL string
	client  *http.Client
}

func (c *httpGitHubClient) GetPullRequest(
	ctx context.Context,
	owner, repo string,
	number int,
) (*PullRequestInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.baseURL, owner, repo, number)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Draft bool `json:"draft"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.Wrapf(ctx, err, "unmarshal pull request %s/%s#%d", owner, repo, number)
	}
	return &PullRequestInfo{Draft: payload.Draft, HeadSHA: payload.Head.SHA}, nil
}

func (c *httpGitHubClient) ListPullRequestFiles(
	ctx context.Context,
	owner, repo string,
	number int,
) ([]string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=100", c.baseURL, owner, repo, number)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var payload []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.Wrapf(
			ctx,
			err,
			"unmarshal pull request files %s/%s#%d",
			owner,
			repo,
			number,
		)
	}
	files := make([]string, 0, len(payload))
	for _, f := range payload {
		files = append(files, f.Filename)
	}
	return files, nil
}

// currentToken returns the credential to authenticate with, preferring the live
// process value over the one captured at construction. Installation tokens
// expire after 60 minutes and the execution phase re-mints immediately before
// its push, so ai_review — which runs later in the same process — must read the
// refreshed value rather than the startup one.
func (c *httpGitHubClient) currentToken() string {
	if live := os.Getenv("GH_TOKEN"); live != "" {
		return live
	}
	return c.token
}

// get performs an authenticated GET and returns the body on HTTP 200.
func (c *httpGitHubClient) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "build request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := c.currentToken(); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "github request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf(
			ctx,
			"github returned HTTP %d: %s",
			resp.StatusCode,
			truncate(string(body)),
		)
	}
	return body, nil
}
