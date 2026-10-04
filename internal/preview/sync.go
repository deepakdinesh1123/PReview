package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/deepakdinesh1123/PReview/internal/cloud"
	"github.com/deepakdinesh1123/PReview/internal/github"
)

// GithubSyncCmd reacts to a pull_request event: it deploys the PR when the
// label is present, redeploys on new commits, and tears down on unlabel/close.
type GithubSyncCmd struct {
	CommonFlags
	Label      string `name:"label" help:"PR label that enables a preview" default:"preview" env:"PREVIEW_LABEL"`
	Token      string `name:"github-token" help:"GitHub token used to post the PR comment" env:"GITHUB_TOKEN"`
	Repository string `name:"github-repository" help:"owner/name of the repository" env:"GITHUB_REPOSITORY"`
	EventPath  string `name:"github-event-path" help:"Path of the webhook event payload" env:"GITHUB_EVENT_PATH"`
	APIURL     string `name:"github-api-url" help:"GitHub API base URL (GitHub Enterprise)" env:"GITHUB_API_URL"`
	OutputPath string `name:"github-output" help:"File the Action outputs are appended to" env:"GITHUB_OUTPUT"`
	RunURL     string `name:"run-url" help:"Link to the workflow run, shown in the PR comment" env:"PREVIEW_RUN_URL"`
}

// Run handles one event.
func (c *GithubSyncCmd) Run(ctx context.Context, awsConfig aws.Config, logger *slog.Logger) error {
	if c.EventPath == "" || c.Repository == "" || c.Token == "" {
		return fmt.Errorf("github-sync needs GITHUB_EVENT_PATH, GITHUB_REPOSITORY and GITHUB_TOKEN (run it inside GitHub Actions)")
	}

	ev, err := github.ParseEventFile(c.EventPath)
	if err != nil {
		return err
	}

	owner, repo, ok := strings.Cut(c.Repository, "/")
	if !ok {
		return fmt.Errorf("invalid repository %q, expected owner/name", c.Repository)
	}

	pr := ev.PullRequest.Number
	key := PRKey(owner, repo, pr)
	name := ImageName(c.NamePrefix, key)
	marker := github.Marker(name)
	gh := github.NewClient(c.Token, c.Repository, c.APIURL)
	sha := ev.PullRequest.Head.SHA

	decision, reason := github.Decide(ev, c.Label)
	logger.Info("github sync", "pr", pr, "action", ev.Action, "decision", decision.String(), "reason", reason)

	switch decision {
	case github.Ignore:
		c.writeOutputs(map[string]string{"live": "false"}, logger)
		return nil

	case github.Teardown:
		d := NewDeployer(awsConfig, logger)
		if err := d.Teardown(ctx, c.NamePrefix, key, c.S3Path); err != nil {
			c.comment(ctx, gh, pr, marker, failureBody("Destroying the preview failed", err, c.RunURL), logger)
			return err
		}
		c.comment(ctx, gh, pr, marker, "### 🗑️ Preview destroyed\n\nThe preview environment for this pull request was removed ("+reason+").", logger)
		c.writeOutputs(map[string]string{"live": "false"}, logger)
		return nil
	}

	// Deploy.
	c.comment(ctx, gh, pr, marker, pendingBody(sha, c.RunURL), logger)

	d := NewDeployer(awsConfig, logger)
	res, err := d.Deploy(ctx, DeployRequest{
		Key:         key,
		CommitID:    sha,
		Description: fmt.Sprintf("Preview of %s PR #%d: %s", c.Repository, pr, ev.PullRequest.Title),
		Tags: map[string]string{
			"preview:repository": c.Repository,
			"preview:pr":         fmt.Sprint(pr),
			"preview:branch":     ev.PullRequest.Head.Ref,
		},
		Flags: c.CommonFlags,
	})
	if err != nil {
		c.comment(ctx, gh, pr, marker, failureBody("Deploying the preview failed", err, c.RunURL), logger)
		c.writeOutputs(map[string]string{"live": "false"}, logger)
		return err
	}

	var authToken string
	var authExpiresAt time.Time
	l := cloud.NewLambdaMicroVMHandler(awsConfig, logger)
	tok, err := l.CreateAuthToken(ctx, res.MicroVMID, cloud.MaxAuthTokenMinutes, int32(c.Port))
	if err == nil {
		authToken = tok
		authExpiresAt = time.Now().Add(time.Duration(cloud.MaxAuthTokenMinutes) * time.Minute)
	} else {
		logger.Warn("could not create auth token for comment", "err", err)
	}

	logger.Info("preview is live", "url", res.URL, "microvm_id", res.MicroVMID)
	c.comment(ctx, gh, pr, marker, liveBody(res, sha, c.RunURL, authToken, authExpiresAt, c.Port), logger)
	c.writeOutputs(map[string]string{
		"live":       "true",
		"url":        res.URL,
		"name":       res.Name,
		"microvm_id": res.MicroVMID,
	}, logger)
	return nil
}

// comment never fails the run: a missing comment must not mask a good deploy.
func (c *GithubSyncCmd) comment(ctx context.Context, gh *github.Client, pr int, marker, body string, logger *slog.Logger) {
	if err := gh.UpsertComment(ctx, pr, marker, body); err != nil {
		logger.Warn("could not update the pull request comment", "err", err)
	}
}

func (c *GithubSyncCmd) writeOutputs(outputs map[string]string, logger *slog.Logger) {
	if c.OutputPath == "" {
		return
	}
	f, err := os.OpenFile(c.OutputPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		logger.Warn("could not write action outputs", "err", err)
		return
	}
	defer f.Close()
	for k, v := range outputs {
		fmt.Fprintf(f, "%s=%s\n", k, v)
	}
}

func runLink(runURL string) string {
	if runURL == "" {
		return ""
	}
	return fmt.Sprintf(" ([workflow run](%s))", runURL)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func pendingBody(sha, runURL string) string {
	return fmt.Sprintf("### 🚧 Preview deploying\n\nBuilding commit `%s`%s. This comment updates when the environment is ready.", short(sha), runLink(runURL))
}

func failureBody(title string, err error, runURL string) string {
	return fmt.Sprintf("### ❌ %s%s\n\n```\n%s\n```", title, runLink(runURL), err.Error())
}

func liveBody(res *DeployResult, sha string, runURL string, authToken string, authExpiresAt time.Time, port int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### 🚀 Preview ready%s\n\n", runLink(runURL))
	fmt.Fprintf(&b, "**%s**\n\n", res.URL)
	fmt.Fprintf(&b, "Commit: `%s`\n", short(sha))

	if authToken != "" {
		fmt.Fprintf(&b, "\n> [!NOTE]\n")
		fmt.Fprintf(&b, "> This raw endpoint requires authentication headers and cannot be opened directly in a browser. Use the token below to access it via API clients (like `curl` or Postman) or a browser extension (like ModHeader).\n\n")
		fmt.Fprintf(&b, "**Auth Token (Valid for 60 minutes)**\n")
		fmt.Fprintf(&b, "Expires at: `%s`\n\n", authExpiresAt.Format(time.RFC1123))
		fmt.Fprintf(&b, "```http\n")
		fmt.Fprintf(&b, "X-aws-proxy-auth: %s\n", authToken)
		fmt.Fprintf(&b, "X-aws-proxy-port: %d\n", port)
		fmt.Fprintf(&b, "```\n\n")
		fmt.Fprintf(&b, "*To generate a new token after expiry, simply re-run the latest deployment job in the Actions tab.*\n")
	}

	b.WriteString("\nRemove the label or close the pull request to destroy this environment.")
	return b.String()
}
