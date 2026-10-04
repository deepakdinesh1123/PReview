package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func event(action, labelName string, labels []string, forkHead string) *Event {
	e := &Event{Action: action}
	e.PullRequest.Number = 7
	e.PullRequest.State = "open"
	e.PullRequest.Base.Repo.FullName = "acme/web"
	e.PullRequest.Head.Repo.FullName = "acme/web"
	if forkHead != "" {
		e.PullRequest.Head.Repo.FullName = forkHead
	}
	if labelName != "" {
		e.Label = &struct {
			Name string `json:"name"`
		}{Name: labelName}
	}
	for _, l := range labels {
		e.PullRequest.Labels = append(e.PullRequest.Labels, struct {
			Name string `json:"name"`
		}{Name: l})
	}
	return e
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		ev   *Event
		want Decision
	}{
		{"label added", event("labeled", "preview", []string{"preview"}, ""), Deploy},
		{"label added is case-insensitive", event("labeled", "Preview", nil, ""), Deploy},
		{"other label added", event("labeled", "bug", []string{"bug"}, ""), Ignore},
		{"label removed", event("unlabeled", "preview", nil, ""), Teardown},
		{"other label removed", event("unlabeled", "bug", []string{"preview"}, ""), Ignore},
		{"push to labeled PR", event("synchronize", "", []string{"preview"}, ""), Deploy},
		{"push to unlabeled PR", event("synchronize", "", nil, ""), Ignore},
		{"closed", event("closed", "", []string{"preview"}, ""), Teardown},
		{"reopened with label", event("reopened", "", []string{"preview"}, ""), Deploy},
		{"fork PR is refused", event("labeled", "preview", []string{"preview"}, "evil/web"), Ignore},
		{"fork PR closing is also refused", event("closed", "", nil, "evil/web"), Ignore},
		{"unknown action", event("edited", "", []string{"preview"}, ""), Ignore},
	}
	for _, c := range cases {
		if got, why := Decide(c.ev, "preview"); got != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, got, why, c.want)
		}
	}
}

func TestParseEventFile(t *testing.T) {
	payload := `{"action":"labeled","number":3,"label":{"name":"preview"},
	"pull_request":{"number":3,"title":"t","state":"open",
	"head":{"ref":"feat","sha":"abc123","repo":{"full_name":"a/b"}},
	"base":{"repo":{"full_name":"a/b"}},"labels":[{"name":"preview"}]}}`

	path := t.TempDir() + "/event.json"
	if err := writeFile(path, payload); err != nil {
		t.Fatal(err)
	}
	ev, err := ParseEventFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ev.PullRequest.Head.SHA != "abc123" || !ev.HasLabel("preview") || ev.IsFork() {
		t.Fatalf("unexpected event: %+v", ev)
	}

	if err := writeFile(path, `{"action":"push"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseEventFile(path); err == nil {
		t.Fatal("non-PR payload must be rejected")
	}
}

func TestUpsertComment(t *testing.T) {
	existing := []comment{{ID: 1, Body: "unrelated"}, {ID: 2, Body: Marker("n") + "\nold"}}
	var patched, posted string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing auth header")
		}
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(existing)
		case r.Method == http.MethodPatch:
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			patched = fmt.Sprintf("%s|%s", r.URL.Path, b["body"])
		case r.Method == http.MethodPost:
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			posted = b["body"]
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	c := NewClient("tok", "acme/web", srv.URL)

	if err := c.UpsertComment(context.Background(), 7, Marker("n"), "new"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(patched, "/repos/acme/web/issues/comments/2|") || !strings.Contains(patched, "new") {
		t.Fatalf("existing comment not updated in place: %q", patched)
	}
	if posted != "" {
		t.Fatal("must not post a duplicate comment")
	}

	existing = existing[:1]
	if err := c.UpsertComment(context.Background(), 7, Marker("n"), "fresh"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(posted, "fresh") || !strings.Contains(posted, Marker("n")) {
		t.Fatalf("new comment not posted: %q", posted)
	}
}
