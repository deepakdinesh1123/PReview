package preview

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxNameLen = 64

var invalidNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// sanitize turns arbitrary text (branch names, repo slugs) into something safe
// to use in an image name, an S3 key and a DNS label.
func sanitize(s string) string {
	s = invalidNameChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-_")
}

// ImageName derives the deterministic MicroVM image name for a deployment key.
//
// The name is the only link between a PR/branch and its cloud resources
// (PReview keeps no local state), so it must be stable and unique per key.
// Names longer than the limit are truncated and suffixed with a hash of the
// full key so distinct keys never collide.
func ImageName(prefix, key string) string {
	prefix = sanitize(prefix)
	if prefix == "" {
		prefix = "preview"
	}
	base := prefix + "-" + sanitize(key)

	if len(base) <= maxNameLen {
		return strings.ToLower(base)
	}

	sum := sha1.Sum([]byte(key))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	return strings.ToLower(strings.TrimRight(base[:maxNameLen-len(suffix)], "-_") + suffix)
}

// PRKey is the deployment key for a pull request.
func PRKey(owner, repo string, number int) string {
	return fmt.Sprintf("%s-%s-pr-%d", owner, repo, number)
}

// BranchKey is the deployment key for a branch deployed outside of a PR.
func BranchKey(repository, branch string) string {
	return fmt.Sprintf("%s-%s", repository, branch)
}

// ParseDuration parses Go durations ("90s", "15m", "8h"), plus day suffixes
// ("2d") and "infinite". The result is clamped to max; "infinite" yields max.
// The boolean reports whether clamping changed the requested value.
func ParseDuration(s string, max time.Duration) (d time.Duration, clamped bool, err error) {
	s = strings.TrimSpace(strings.ToLower(s))

	switch {
	case s == "" || s == "infinite" || s == "infinity":
		return max, false, nil
	case strings.HasSuffix(s, "d"):
		days, convErr := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if convErr != nil {
			return 0, false, fmt.Errorf("invalid duration %q", s)
		}
		d = time.Duration(days * float64(24*time.Hour))
	default:
		d, err = time.ParseDuration(s)
		if err != nil {
			return 0, false, fmt.Errorf("invalid duration %q: %w", s, err)
		}
	}

	if d <= 0 {
		return 0, false, fmt.Errorf("duration %q must be positive", s)
	}
	if d > max {
		return max, true, nil
	}
	return d, false, nil
}
