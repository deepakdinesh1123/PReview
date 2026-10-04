package preview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/deepakdinesh1123/PReview/internal/cloud"
)

// Deployer orchestrates preview deployments. All state lives in AWS: a
// deployment is identified purely by its image name (see ImageName).
type Deployer struct {
	AWS    aws.Config
	Lambda *cloud.LambdaMicroVMHandler
	Logger *slog.Logger
}

// NewDeployer builds a Deployer.
func NewDeployer(awsConfig aws.Config, logger *slog.Logger) *Deployer {
	return &Deployer{
		AWS:    awsConfig,
		Lambda: cloud.NewLambdaMicroVMHandler(awsConfig, logger),
		Logger: logger,
	}
}

// DeployRequest is one deployment of a key at a commit.
type DeployRequest struct {
	Key         string // stable identity: a PR or a branch
	CommitID    string
	Description string
	Tags        map[string]string
	Flags       CommonFlags
}

// DeployResult describes a finished deployment.
type DeployResult struct {
	Name         string
	ImageARN     string
	ImageVersion string
	MicroVMID    string
	Endpoint     string // raw MicroVM endpoint (requires an auth header)
	URL          string // raw endpoint URL
}

// ResolvedDurations are the validated lifecycle settings of a deployment.
type ResolvedDurations struct {
	TTL, Idle, Suspended time.Duration
}

// ResolveDurations validates the ttl/idle/suspended flags against platform limits.
func ResolveDurations(f CommonFlags, logger *slog.Logger) (ResolvedDurations, error) {
	max := time.Duration(cloud.MaxMicroVMDurationSeconds) * time.Second

	ttl, clamped, err := ParseDuration(f.TTL, max)
	if err != nil {
		return ResolvedDurations{}, fmt.Errorf("--ttl: %w", err)
	}
	if clamped {
		logger.Warn("ttl exceeds the 8h platform limit; clamped", "ttl", ttl)
	}

	idle, _, err := ParseDuration(f.IdleDuration, ttl)
	if err != nil {
		return ResolvedDurations{}, fmt.Errorf("--idle-duration: %w", err)
	}
	suspended, _, err := ParseDuration(f.SuspendedFor, max)
	if err != nil {
		return ResolvedDurations{}, fmt.Errorf("--suspended-duration: %w", err)
	}
	return ResolvedDurations{TTL: ttl, Idle: idle, Suspended: suspended}, nil
}

// Deploy builds (or rebuilds) the image for the key and runs a fresh MicroVM
// from it. The previous MicroVM keeps serving until the new one is running and
// is terminated afterwards, so the shared link never goes dark mid-deploy.
func (d *Deployer) Deploy(ctx context.Context, req DeployRequest) (*DeployResult, error) {
	f := req.Flags
	name := ImageName(f.NamePrefix, req.Key)
	log := d.Logger.With("name", name, "commit", req.CommitID)

	durations, err := ResolveDurations(f, d.Logger)
	if err != nil {
		return nil, err
	}

	// 1. Package the app and upload it.
	zipPath, err := ZipApp(f.AppPath)
	if err != nil {
		return nil, err
	}
	defer os.Remove(zipPath)

	object := fmt.Sprintf("%s/%s.zip", name, sanitize(req.CommitID))
	artifactURI, err := cloud.UploadFile(ctx, d.AWS, f.S3Path, object, zipPath)
	if err != nil {
		return nil, err
	}
	log.Info("build context uploaded", "artifact", artifactURI)

	// 2. Create the image, or build a new version of the existing one.
	tags := map[string]string{
		"preview:managed": "true",
		"preview:key":     req.Key,
		"preview:commit":  req.CommitID,
	}
	for k, v := range req.Tags {
		tags[k] = v
	}
	spec := cloud.ImageSpec{
		Name:             name,
		Description:      truncate(req.Description, 256),
		BaseImageARN:     f.BaseImageARN,
		BuildRoleARN:     f.BuildRoleARN,
		ArtifactURI:      artifactURI,
		MemoryMiB:        int32(f.Memory),
		EgressConnectors: f.EgressNetwork,
		Tags:             tags,
	}

	var imageARN, version string
	existing, err := d.Lambda.FindImage(ctx, name)
	switch {
	case err == nil:
		imageARN = aws.ToString(existing.ImageArn)
		log.Info("updating existing image", "image_arn", imageARN)
		if version, err = d.Lambda.UpdateImage(ctx, imageARN, spec); err != nil {
			return nil, err
		}
		if err := d.Lambda.TagImage(ctx, imageARN, tags); err != nil {
			log.Warn("could not refresh image tags", "err", err)
		}
	case errors.Is(err, cloud.ErrImageNotFound):
		log.Info("creating image")
		if imageARN, version, err = d.Lambda.CreateImage(ctx, spec); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	log.Info("waiting for image build", "image_arn", imageARN, "version", version)
	if err := d.Lambda.WaitImageVersion(ctx, imageARN, version); err != nil {
		return nil, err
	}

	// 3. Start the new MicroVM (or reuse an existing one if the version matches).
	previous, err := d.Lambda.ListLiveMicroVMs(ctx, imageARN)
	if err != nil {
		return nil, err
	}

	var vm *cloud.MicroVM
	for _, old := range previous {
		if old.ImageVersion == version {
			log.Info("reusing existing microVM for version", "microvm_id", old.ID, "version", version)
			// Need GetMicroVM to fetch the Endpoint which ListLiveMicroVMs omits
			if full, err := d.Lambda.GetMicroVM(ctx, old.ID); err == nil {
				vm = full
				break
			}
		}
	}

	if vm == nil {
		vm, err = d.Lambda.RunMicroVM(ctx, cloud.RunSpec{
			ImageARN:           imageARN,
			ImageVersion:       version,
			ExecutionRoleARN:   f.ExecutionRoleARN,
			MaxDurationSeconds: int32(durations.TTL.Seconds()),
			MaxIdleSeconds:     int32(durations.Idle.Seconds()),
			SuspendedSeconds:   int32(durations.Suspended.Seconds()),
			IngressConnectors:  f.IngressNetwork,
			EgressConnectors:   f.EgressNetwork,
		})
		if err != nil {
			return nil, err
		}
		log.Info("microVM started", "microvm_id", vm.ID)
	}

	if err := d.Lambda.WaitMicroVMRunning(ctx, vm.ID); err != nil {
		return nil, err
	}

	// 4. Retire older MicroVMs now that the target one is serving.
	for _, old := range previous {
		if old.ID == vm.ID {
			continue
		}
		if err := d.Lambda.TerminateMicroVM(ctx, old.ID); err != nil {
			log.Warn("could not terminate previous microVM", "microvm_id", old.ID, "err", err)
		}
	}

	return &DeployResult{
		Name:         name,
		ImageARN:     imageARN,
		ImageVersion: version,
		MicroVMID:    vm.ID,
		Endpoint:     vm.Endpoint,
		URL:          vm.Endpoint,
	}, nil
}

// Teardown terminates every MicroVM of the key, deletes its image and, when
// s3Path is set, its uploaded build artifacts. It is idempotent.
func (d *Deployer) Teardown(ctx context.Context, namePrefix, key, s3Path string) error {
	name := ImageName(namePrefix, key)
	log := d.Logger.With("name", name)

	img, err := d.Lambda.FindImage(ctx, name)
	switch {
	case errors.Is(err, cloud.ErrImageNotFound):
		log.Info("no image found; nothing to tear down")
	case err != nil:
		return err
	default:
		imageARN := aws.ToString(img.ImageArn)

		live, err := d.Lambda.ListLiveMicroVMs(ctx, imageARN)
		if err != nil {
			return err
		}
		for _, vm := range live {
			log.Info("terminating microVM", "microvm_id", vm.ID)
			if err := d.Lambda.TerminateMicroVM(ctx, vm.ID); err != nil {
				return err
			}
		}
		if err := d.Lambda.WaitNoLiveMicroVMs(ctx, imageARN); err != nil {
			return err
		}

		if err := d.deleteImageWithRetry(ctx, imageARN); err != nil {
			return err
		}
		log.Info("image deleted", "image_arn", imageARN)
	}

	if s3Path != "" {
		n, err := cloud.DeletePrefix(ctx, d.AWS, s3Path, name)
		if err != nil {
			// Leftover artifacts are only clutter; do not fail the teardown.
			log.Warn("could not clean up build artifacts", "err", err)
		} else {
			log.Info("build artifacts removed", "objects", n)
		}
	}
	return nil
}

// deleteImageWithRetry tolerates the short window where terminated MicroVMs
// still reference the image.
func (d *Deployer) deleteImageWithRetry(ctx context.Context, imageARN string) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if err = d.Lambda.DeleteImage(ctx, imageARN); err == nil {
			return nil
		}
		d.Logger.Debug("image delete failed; retrying", "attempt", attempt+1, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
