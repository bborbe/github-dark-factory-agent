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
	"time"

	"github.com/bborbe/errors"
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
		client:  &http.Client{Timeout: 15 * time.Second},
	}
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

// get performs an authenticated GET and returns the body on HTTP 200.
func (c *httpGitHubClient) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "build request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
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
