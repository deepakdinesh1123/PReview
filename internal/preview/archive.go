package preview

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skipDirs are never useful inside an image build context.
var skipDirs = map[string]bool{
	".git":         true,
	".github":      true,
	"node_modules": true,
}

// ZipApp archives the application directory into a temporary zip file with the
// Dockerfile at the archive root (a MicroVM image build requirement) and
// returns the zip's path. The caller owns the file and should remove it.
func ZipApp(appPath string) (string, error) {
	info, err := os.Stat(appPath)
	if err != nil {
		return "", fmt.Errorf("app path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("app path %q is not a directory", appPath)
	}
	if _, err := os.Stat(filepath.Join(appPath, "Dockerfile")); err != nil {
		return "", fmt.Errorf("no Dockerfile found at the root of %q: %w", appPath, err)
	}

	out, err := os.CreateTemp("", "preview-*.zip")
	if err != nil {
		return "", err
	}
	zipPath := out.Name()

	fail := func(err error) (string, error) {
		out.Close()
		os.Remove(zipPath)
		return "", err
	}

	zw := zip.NewWriter(out)

	walkErr := filepath.WalkDir(appPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(appPath, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Only regular files: skip symlinks, sockets, devices.
		if !d.Type().IsRegular() {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Method = zip.Deflate

		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(w, f)
		return err
	})
	if walkErr != nil {
		return fail(fmt.Errorf("archiving %q: %w", appPath, walkErr))
	}

	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(zipPath)
		return "", err
	}

	if strings.TrimSpace(zipPath) == "" {
		return "", fmt.Errorf("could not create archive")
	}
	return zipPath, nil
}
