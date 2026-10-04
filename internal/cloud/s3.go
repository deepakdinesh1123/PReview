package cloud

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ParseS3URI splits s3://bucket/optional/prefix into bucket and key prefix.
func ParseS3URI(s3URI string) (bucket, key string, err error) {
	u, err := url.Parse(s3URI)
	if err != nil {
		return "", "", err
	}

	if u.Scheme != "s3" {
		return "", "", fmt.Errorf("invalid scheme %q, expected s3", u.Scheme)
	}

	bucket = u.Host
	key = strings.Trim(u.Path, "/")

	if bucket == "" {
		return "", "", fmt.Errorf("missing bucket name")
	}

	return bucket, key, nil
}

// objectKey joins the configured prefix and an object name without ever
// producing a leading slash.
func objectKey(prefix, name string) string {
	return strings.TrimPrefix(path.Join(prefix, name), "/")
}

// UploadFile uploads filePath to <bucketURI>/<name> and returns the s3:// URI.
func UploadFile(ctx context.Context, awsConfig aws.Config, bucketURI, name, filePath string) (string, error) {
	bucket, prefix, err := ParseS3URI(bucketURI)
	if err != nil {
		return "", err
	}

	key := objectKey(prefix, name)

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	_, err = s3.NewFromConfig(awsConfig).PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	if err != nil {
		return "", fmt.Errorf("uploading to s3://%s/%s: %w", bucket, key, err)
	}

	return fmt.Sprintf("s3://%s/%s", bucket, key), nil
}

// DeletePrefix removes every object stored under <bucketURI>/<name>/ and
// returns how many objects were deleted.
func DeletePrefix(ctx context.Context, awsConfig aws.Config, bucketURI, name string) (int, error) {
	bucket, prefix, err := ParseS3URI(bucketURI)
	if err != nil {
		return 0, err
	}

	client := s3.NewFromConfig(awsConfig)
	listPrefix := objectKey(prefix, name) + "/"
	deleted := 0

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(listPrefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return deleted, fmt.Errorf("listing s3://%s/%s: %w", bucket, listPrefix, err)
		}
		if len(page.Contents) == 0 {
			continue
		}

		ids := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			ids = append(ids, s3types.ObjectIdentifier{Key: obj.Key})
		}
		if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		}); err != nil {
			return deleted, fmt.Errorf("deleting objects under s3://%s/%s: %w", bucket, listPrefix, err)
		}
		deleted += len(ids)
	}

	return deleted, nil
}
