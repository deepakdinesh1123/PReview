package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal GitHub REST client (net/http only, no extra dependency).
type Client struct {
	token   string
	repo    string // owner/name
	baseURL string
	http    *http.Client
}

// NewClient builds a client for repo ("owner/name"). baseURL may be empty for
// github.com; set it for GitHub Enterprise (GITHUB_API_URL) or tests.
func NewClient(token, repo, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{
		token:   token,
		repo:    repo,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Marker returns the hidden marker that identifies the status comment of a
// deployment, so it is updated in place instead of spamming the PR.
func Marker(name string) string {
	return fmt.Sprintf("<!-- preview:%s -->", name)
}

// UpsertComment creates or updates the PR comment that carries marker.
func (c *Client) UpsertComment(ctx context.Context, pr int, marker, body string) error {
	body = marker + "\n" + body
	listPath := fmt.Sprintf("/repos/%s/issues/%d/comments", c.repo, pr)

	for page := 1; ; page++ {
		var comments []comment
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", listPath, page), nil, &comments); err != nil {
			return err
		}
		for _, cm := range comments {
			if strings.Contains(cm.Body, marker) {
				return c.do(ctx, http.MethodPatch,
					fmt.Sprintf("/repos/%s/issues/comments/%d", c.repo, cm.ID),
					map[string]string{"body": body}, nil)
			}
		}
		if len(comments) < 100 {
			break
		}
	}

	return c.do(ctx, http.MethodPost, listPath, map[string]string{"body": body}, nil)
}
