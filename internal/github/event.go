// Package github contains the small slice of GitHub integration PReview needs:
// interpreting pull_request events and maintaining a single status comment.
package github

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Event is the subset of the pull_request webhook payload PReview reads.
type Event struct {
	Action string `json:"action"`
	Number int    `json:"number"`
	Label  *struct {
		Name string `json:"name"`
	} `json:"label"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Head   struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
}

// ParseEventFile reads a webhook payload (as found at $GITHUB_EVENT_PATH).
func ParseEventFile(path string) (*Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading event payload: %w", err)
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("parsing event payload: %w", err)
	}
	if ev.PullRequest.Number == 0 {
		ev.PullRequest.Number = ev.Number
	}
	if ev.PullRequest.Number == 0 {
		return nil, fmt.Errorf("event payload is not a pull_request event")
	}
	return &ev, nil
}

// HasLabel reports whether the PR currently carries the label.
func (e *Event) HasLabel(label string) bool {
	for _, l := range e.PullRequest.Labels {
		if strings.EqualFold(l.Name, label) {
			return true
		}
	}
	return false
}

// IsFork reports whether the PR comes from a fork. Fork PRs never receive
// repository secrets, so deploying them would fail anyway - and must not be
// attempted with credentials.
func (e *Event) IsFork() bool {
	head, base := e.PullRequest.Head.Repo.FullName, e.PullRequest.Base.Repo.FullName
	return head != "" && base != "" && !strings.EqualFold(head, base)
}

// Decision is what github-sync should do for an event.
type Decision int

const (
	Ignore Decision = iota
	Deploy
	Teardown
)

func (d Decision) String() string {
	return [...]string{"ignore", "deploy", "teardown"}[d]
}

// Decide maps an event to an action, following pullpreview's label model:
// the label turns a preview on, new commits redeploy it, removing the label
// or closing the PR destroys it.
func Decide(e *Event, label string) (Decision, string) {
	if e.IsFork() {
		return Ignore, "pull request comes from a fork; previews are only built for same-repository branches"
	}

	switch e.Action {
	case "closed":
		return Teardown, "pull request closed"

	case "unlabeled":
		if e.Label != nil && strings.EqualFold(e.Label.Name, label) {
			return Teardown, fmt.Sprintf("label %q removed", label)
		}
		return Ignore, "an unrelated label was removed"

	case "labeled":
		if e.Label != nil && strings.EqualFold(e.Label.Name, label) {
			return Deploy, fmt.Sprintf("label %q added", label)
		}
		return Ignore, "an unrelated label was added"

	case "opened", "reopened", "synchronize", "ready_for_review":
		if e.PullRequest.State == "closed" {
			return Ignore, "pull request is closed"
		}
		if e.HasLabel(label) {
			return Deploy, "new commits on a labeled pull request"
		}
		return Ignore, fmt.Sprintf("pull request does not have the %q label", label)
	}

	return Ignore, fmt.Sprintf("unsupported action %q", e.Action)
}
