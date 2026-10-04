package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/deepakdinesh1123/PReview/internal/preview"
)

// CLI is the command tree of the preview binary.
type CLI struct {
	Debug bool `help:"Enable debug logging" env:"PREVIEW_DEBUG"`

	GithubSync preview.GithubSyncCmd `cmd:"" name:"github-sync" help:"Sync a GitHub pull request event: deploy, redeploy or destroy its preview"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var cli CLI
	kctx := kong.Parse(
		&cli,
		kong.Name("preview"),
		kong.Description("Preview environments on AWS Lambda MicroVMs"),
		kong.UsageOnError(),
	)

	level := slog.LevelInfo
	if cli.Debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	awsConfig, err := config.LoadDefaultConfig(ctx)
	kctx.FatalIfErrorf(err)
	if awsConfig.Region == "" {
		kctx.FatalIfErrorf(fmt.Errorf("no AWS region configured: set AWS_REGION"))
	}

	kctx.BindTo(ctx, (*context.Context)(nil))
	kctx.Bind(awsConfig, logger)

	// Propagate failures so CI marks the run as failed.
	kctx.FatalIfErrorf(kctx.Run())
}
