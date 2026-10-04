// Package cloud wraps the AWS services PReview depends on (Lambda MicroVMs, S3).
package cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
)

const (
	// AuthHeader is the header every request to a MicroVM endpoint must carry.
	AuthHeader = "X-aws-proxy-auth"
	// PortHeader selects the port inside the MicroVM the request is routed to.
	PortHeader = "X-aws-proxy-port"

	// MaxMicroVMDurationSeconds is the platform limit for RunMicrovm's
	// MaximumDurationInSeconds (8 hours).
	MaxMicroVMDurationSeconds = 28800
	// MaxAuthTokenMinutes is the platform limit for auth token lifetime.
	MaxAuthTokenMinutes = 60
)

// ErrImageNotFound is returned when no MicroVM image has the requested name.
var ErrImageNotFound = errors.New("microVM image not found")

// LambdaMicroVMHandler is a thin, opinionated client for the Lambda MicroVMs API.
type LambdaMicroVMHandler struct {
	client *lambdamicrovms.Client
	logger *slog.Logger

	// ImagePollInterval controls how often image builds are polled.
	ImagePollInterval time.Duration
	// MicroVMPollInterval controls how often MicroVM state is polled.
	MicroVMPollInterval time.Duration
}

// NewLambdaMicroVMHandler builds a handler from an AWS config.
func NewLambdaMicroVMHandler(awsConfig aws.Config, logger *slog.Logger) *LambdaMicroVMHandler {
	return &LambdaMicroVMHandler{
		client:              lambdamicrovms.NewFromConfig(awsConfig),
		logger:              logger,
		ImagePollInterval:   5 * time.Second,
		MicroVMPollInterval: 2 * time.Second,
	}
}

// ImageSpec describes a MicroVM image to build.
type ImageSpec struct {
	Name             string
	Description      string
	BaseImageARN     string
	BuildRoleARN     string
	ArtifactURI      string // s3://bucket/key.zip containing a Dockerfile at its root
	MemoryMiB        int32
	EgressConnectors []string
	Tags             map[string]string
}

// RunSpec describes a MicroVM to start from an image version.
type RunSpec struct {
	ImageARN           string
	ImageVersion       string
	ExecutionRoleARN   string
	MaxDurationSeconds int32
	MaxIdleSeconds     int32
	SuspendedSeconds   int32
	IngressConnectors  []string
	EgressConnectors   []string
}

// MicroVM is the subset of MicroVM data PReview cares about.
type MicroVM struct {
	ID           string
	Endpoint     string
	State        string
	ImageARN     string
	ImageVersion string
	StartedAt    time.Time
}

// IsLive reports whether a MicroVM state is one that still counts as a deployment.
func IsLive(state string) bool {
	switch types.MicrovmState(state) {
	case types.MicrovmStatePending, types.MicrovmStateRunning,
		types.MicrovmStateSuspending, types.MicrovmStateSuspended:
		return true
	}
	return false
}

// FindImage returns the image whose name matches exactly, or ErrImageNotFound.
func (l *LambdaMicroVMHandler) FindImage(ctx context.Context, name string) (*types.MicrovmImageSummary, error) {
	var next *string
	for {
		out, err := l.client.ListMicrovmImages(ctx, &lambdamicrovms.ListMicrovmImagesInput{
			NameFilter: aws.String(name),
			NextToken:  next,
		})
		if err != nil {
			return nil, fmt.Errorf("listing microVM images: %w", err)
		}
		for i := range out.Items {
			if aws.ToString(out.Items[i].Name) == name {
				item := out.Items[i]
				return &item, nil
			}
		}
		if out.NextToken == nil {
			return nil, ErrImageNotFound
		}
		next = out.NextToken
	}
}

// ListImages returns every image whose name contains the given filter.
func (l *LambdaMicroVMHandler) ListImages(ctx context.Context, nameFilter string) ([]types.MicrovmImageSummary, error) {
	var (
		next  *string
		items []types.MicrovmImageSummary
	)
	for {
		out, err := l.client.ListMicrovmImages(ctx, &lambdamicrovms.ListMicrovmImagesInput{
			NameFilter: aws.String(nameFilter),
			NextToken:  next,
		})
		if err != nil {
			return nil, fmt.Errorf("listing microVM images: %w", err)
		}
		items = append(items, out.Items...)
		if out.NextToken == nil {
			return items, nil
		}
		next = out.NextToken
	}
}

func resources(mem int32) []types.Resources {
	if mem <= 0 {
		return nil
	}
	return []types.Resources{{MinimumMemoryInMiB: aws.Int32(mem)}}
}

// CreateImage starts building a brand new image and returns its ARN and version.
func (l *LambdaMicroVMHandler) CreateImage(ctx context.Context, spec ImageSpec) (imageARN, imageVersion string, err error) {
	out, err := l.client.CreateMicrovmImage(ctx, &lambdamicrovms.CreateMicrovmImageInput{
		Name:                    aws.String(spec.Name),
		Description:             aws.String(spec.Description),
		BaseImageArn:            aws.String(spec.BaseImageARN),
		BuildRoleArn:            aws.String(spec.BuildRoleARN),
		CodeArtifact:            &types.CodeArtifactMemberUri{Value: spec.ArtifactURI},
		Resources:               resources(spec.MemoryMiB),
		EgressNetworkConnectors: spec.EgressConnectors,
		Tags:                    spec.Tags,
	})
	if err != nil {
		return "", "", fmt.Errorf("creating microVM image %q: %w", spec.Name, err)
	}
	return aws.ToString(out.ImageArn), aws.ToString(out.ImageVersion), nil
}

// UpdateImage builds a new version of an existing image and returns the version.
func (l *LambdaMicroVMHandler) UpdateImage(ctx context.Context, imageARN string, spec ImageSpec) (imageVersion string, err error) {
	out, err := l.client.UpdateMicrovmImage(ctx, &lambdamicrovms.UpdateMicrovmImageInput{
		ImageIdentifier:         aws.String(imageARN),
		Description:             aws.String(spec.Description),
		BaseImageArn:            aws.String(spec.BaseImageARN),
		BuildRoleArn:            aws.String(spec.BuildRoleARN),
		CodeArtifact:            &types.CodeArtifactMemberUri{Value: spec.ArtifactURI},
		Resources:               resources(spec.MemoryMiB),
		EgressNetworkConnectors: spec.EgressConnectors,
	})
	if err != nil {
		return "", fmt.Errorf("updating microVM image %q: %w", spec.Name, err)
	}
	return aws.ToString(out.ImageVersion), nil
}

// TagImage adds or overwrites tags on an image.
func (l *LambdaMicroVMHandler) TagImage(ctx context.Context, imageARN string, tags map[string]string) error {
	_, err := l.client.TagResource(ctx, &lambdamicrovms.TagResourceInput{
		Resource: aws.String(imageARN),
		Tags:     tags,
	})
	if err != nil {
		return fmt.Errorf("tagging microVM image: %w", err)
	}
	return nil
}

// WaitImageVersion blocks until the given image version finished building.
// Waiting on the version (rather than the image) avoids racing with the image
// still reporting its previous state right after an update.
func (l *LambdaMicroVMHandler) WaitImageVersion(ctx context.Context, imageARN, version string) error {
	ticker := time.NewTicker(l.ImagePollInterval)
	defer ticker.Stop()

	for {
		out, err := l.client.GetMicrovmImageVersion(ctx, &lambdamicrovms.GetMicrovmImageVersionInput{
			ImageIdentifier: aws.String(imageARN),
			ImageVersion:    aws.String(version),
		})
		if err != nil {
			return fmt.Errorf("fetching image build status: %w", err)
		}

		l.logger.Debug("image version state", "state", out.State, "version", version)

		switch out.State {
		case types.MicrovmImageVersionStateSuccessful:
			return nil
		case types.MicrovmImageVersionStatePending, types.MicrovmImageVersionStateInProgress:
			// keep waiting
		case types.MicrovmImageVersionStateFailed:
			return fmt.Errorf("microVM image build failed: %s", stateReason(out.StateReason))
		default:
			return fmt.Errorf("microVM image version is in unexpected state %s: %s", out.State, stateReason(out.StateReason))
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for image build: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func stateReason(r *string) string {
	if r == nil || *r == "" {
		return "no reason provided"
	}
	return *r
}

// DeleteImage deletes an image (and with it all its versions).
func (l *LambdaMicroVMHandler) DeleteImage(ctx context.Context, imageARN string) error {
	_, err := l.client.DeleteMicrovmImage(ctx, &lambdamicrovms.DeleteMicrovmImageInput{
		ImageIdentifier: aws.String(imageARN),
	})
	if err != nil {
		return fmt.Errorf("deleting microVM image: %w", err)
	}
	return nil
}

// RunMicroVM starts a MicroVM from an image version.
func (l *LambdaMicroVMHandler) RunMicroVM(ctx context.Context, spec RunSpec) (*MicroVM, error) {
	in := &lambdamicrovms.RunMicrovmInput{
		ImageIdentifier:          aws.String(spec.ImageARN),
		ImageVersion:             aws.String(spec.ImageVersion),
		MaximumDurationInSeconds: aws.Int32(spec.MaxDurationSeconds),
		IngressNetworkConnectors: spec.IngressConnectors,
		EgressNetworkConnectors:  spec.EgressConnectors,
		IdlePolicy: &types.IdlePolicy{
			AutoResumeEnabled:        aws.Bool(true),
			MaxIdleDurationSeconds:   aws.Int32(spec.MaxIdleSeconds),
			SuspendedDurationSeconds: aws.Int32(spec.SuspendedSeconds),
		},
	}
	if spec.ExecutionRoleARN != "" {
		in.ExecutionRoleArn = aws.String(spec.ExecutionRoleARN)
	}

	out, err := l.client.RunMicrovm(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("running microVM: %w", err)
	}

	return &MicroVM{
		ID:           aws.ToString(out.MicrovmId),
		Endpoint:     aws.ToString(out.Endpoint),
		State:        string(out.State),
		ImageARN:     aws.ToString(out.ImageArn),
		ImageVersion: aws.ToString(out.ImageVersion),
		StartedAt:    aws.ToTime(out.StartedAt),
	}, nil
}

// GetMicroVM fetches a single MicroVM by ID.
func (l *LambdaMicroVMHandler) GetMicroVM(ctx context.Context, microVMID string) (*MicroVM, error) {
	out, err := l.client.GetMicrovm(ctx, &lambdamicrovms.GetMicrovmInput{
		MicrovmIdentifier: aws.String(microVMID),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching microVM %s: %w", microVMID, err)
	}
	return &MicroVM{
		ID:           aws.ToString(out.MicrovmId),
		Endpoint:     aws.ToString(out.Endpoint),
		State:        string(out.State),
		ImageARN:     aws.ToString(out.ImageArn),
		ImageVersion: aws.ToString(out.ImageVersion),
		StartedAt:    aws.ToTime(out.StartedAt),
	}, nil
}

// WaitMicroVMRunning blocks until the MicroVM reaches RUNNING.
func (l *LambdaMicroVMHandler) WaitMicroVMRunning(ctx context.Context, microVMID string) error {
	ticker := time.NewTicker(l.MicroVMPollInterval)
	defer ticker.Stop()

	for {
		vm, err := l.GetMicroVM(ctx, microVMID)
		if err != nil {
			return err
		}

		switch types.MicrovmState(vm.State) {
		case types.MicrovmStateRunning:
			return nil
		case types.MicrovmStatePending:
			// keep waiting
		default:
			return fmt.Errorf("microVM %s entered state %s while starting", microVMID, vm.State)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for microVM: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// ListMicroVMs lists every MicroVM of an image, in any state.
func (l *LambdaMicroVMHandler) ListMicroVMs(ctx context.Context, imageARN string) ([]MicroVM, error) {
	var (
		next *string
		vms  []MicroVM
	)
	for {
		out, err := l.client.ListMicrovms(ctx, &lambdamicrovms.ListMicrovmsInput{
			ImageIdentifier: aws.String(imageARN),
			NextToken:       next,
		})
		if err != nil {
			return nil, fmt.Errorf("listing microVMs: %w", err)
		}
		for _, it := range out.Items {
			vms = append(vms, MicroVM{
				ID:           aws.ToString(it.MicrovmId),
				State:        string(it.State),
				ImageARN:     aws.ToString(it.ImageArn),
				ImageVersion: aws.ToString(it.ImageVersion),
				StartedAt:    aws.ToTime(it.StartedAt),
			})
		}
		if out.NextToken == nil {
			return vms, nil
		}
		next = out.NextToken
	}
}

// ListLiveMicroVMs lists MicroVMs of an image that are not terminating/terminated.
func (l *LambdaMicroVMHandler) ListLiveMicroVMs(ctx context.Context, imageARN string) ([]MicroVM, error) {
	all, err := l.ListMicroVMs(ctx, imageARN)
	if err != nil {
		return nil, err
	}
	live := all[:0]
	for _, vm := range all {
		if IsLive(vm.State) {
			live = append(live, vm)
		}
	}
	return live, nil
}

// LatestLiveMicroVM returns the newest live MicroVM of an image, including its
// endpoint, or nil when the image has none.
func (l *LambdaMicroVMHandler) LatestLiveMicroVM(ctx context.Context, imageARN string) (*MicroVM, error) {
	live, err := l.ListLiveMicroVMs(ctx, imageARN)
	if err != nil {
		return nil, err
	}
	if len(live) == 0 {
		return nil, nil
	}
	sort.Slice(live, func(i, j int) bool { return live[i].StartedAt.After(live[j].StartedAt) })
	return l.GetMicroVM(ctx, live[0].ID)
}

// TerminateMicroVM terminates a MicroVM.
func (l *LambdaMicroVMHandler) TerminateMicroVM(ctx context.Context, microVMID string) error {
	_, err := l.client.TerminateMicrovm(ctx, &lambdamicrovms.TerminateMicrovmInput{
		MicrovmIdentifier: aws.String(microVMID),
	})
	if err != nil {
		return fmt.Errorf("terminating microVM %s: %w", microVMID, err)
	}
	return nil
}

// WaitNoLiveMicroVMs blocks until an image has no live MicroVMs left.
func (l *LambdaMicroVMHandler) WaitNoLiveMicroVMs(ctx context.Context, imageARN string) error {
	ticker := time.NewTicker(l.MicroVMPollInterval)
	defer ticker.Stop()

	for {
		live, err := l.ListLiveMicroVMs(ctx, imageARN)
		if err != nil {
			return err
		}
		if len(live) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for microVMs to terminate: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// CreateAuthToken mints a token for the X-aws-proxy-auth header granting
// access to a single port of a MicroVM.
func (l *LambdaMicroVMHandler) CreateAuthToken(ctx context.Context, microVMID string, minutes int32, port int32) (string, error) {
	if minutes <= 0 || minutes > MaxAuthTokenMinutes {
		minutes = MaxAuthTokenMinutes
	}
	out, err := l.client.CreateMicrovmAuthToken(ctx, &lambdamicrovms.CreateMicrovmAuthTokenInput{
		MicrovmIdentifier:   aws.String(microVMID),
		ExpirationInMinutes: aws.Int32(minutes),
		AllowedPorts:        []types.PortSpecification{&types.PortSpecificationMemberPort{Value: port}},
	})
	if err != nil {
		return "", fmt.Errorf("creating auth token for microVM %s: %w", microVMID, err)
	}

	if tok, ok := out.AuthToken[AuthHeader]; ok {
		return tok, nil
	}
	// Be lenient about the exact map key: a single-entry map is unambiguous.
	if len(out.AuthToken) == 1 {
		for _, tok := range out.AuthToken {
			return tok, nil
		}
	}
	keys := make([]string, 0, len(out.AuthToken))
	for k := range out.AuthToken {
		keys = append(keys, k)
	}
	return "", fmt.Errorf("auth token response has no %q entry (keys: %s)", AuthHeader, strings.Join(keys, ","))
}
