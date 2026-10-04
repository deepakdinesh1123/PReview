package preview

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImageName(t *testing.T) {
	t.Run("sanitizes and lowercases", func(t *testing.T) {
		got := ImageName("preview", "Acme-Web-pr-12")
		if got != "preview-acme-web-pr-12" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("branch slashes become dashes", func(t *testing.T) {
		got := ImageName("preview", BranchKey("app", "feature/login page"))
		if got != "preview-app-feature-login-page" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("long keys are truncated but stay unique", func(t *testing.T) {
		a := ImageName("preview", strings.Repeat("a", 100)+"-1")
		b := ImageName("preview", strings.Repeat("a", 100)+"-2")
		if len(a) > maxNameLen || len(b) > maxNameLen {
			t.Fatalf("names too long: %d, %d", len(a), len(b))
		}
		if a == b {
			t.Fatal("distinct keys collided")
		}
	})

	t.Run("is deterministic", func(t *testing.T) {
		if ImageName("p", "k") != ImageName("p", "k") {
			t.Fatal("not deterministic")
		}
	})
}

func TestParseDuration(t *testing.T) {
	max := 8 * time.Hour
	cases := []struct {
		in      string
		want    time.Duration
		clamped bool
		wantErr bool
	}{
		{"15m", 15 * time.Minute, false, false},
		{"2h", 2 * time.Hour, false, false},
		{"infinite", max, false, false},
		{"", max, false, false},
		{"1d", max, true, false},
		{"0.1d", 144 * time.Minute, false, false},
		{"99h", max, true, false},
		{"-5m", 0, false, true},
		{"soon", 0, false, true},
	}
	for _, c := range cases {
		got, clamped, err := ParseDuration(c.in, max)
		if (err != nil) != c.wantErr {
			t.Fatalf("%q: err=%v", c.in, err)
		}
		if err == nil && (got != c.want || clamped != c.clamped) {
			t.Fatalf("%q: got %v clamped=%v, want %v clamped=%v", c.in, got, clamped, c.want, c.clamped)
		}
	}
}
func TestZipApp(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Dockerfile", "FROM scratch\n")
	write("src/main.go", "package main\n")
	write(".git/HEAD", "ref")
	write("node_modules/x/index.js", "x")

	zipPath, err := ZipApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(zipPath)

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if !got["Dockerfile"] || !got["src/main.go"] {
		t.Fatalf("missing expected entries: %v", got)
	}
	if got[".git/HEAD"] || got["node_modules/x/index.js"] {
		t.Fatalf("excluded entries were archived: %v", got)
	}
}

func TestZipAppRequiresDockerfile(t *testing.T) {
	if _, err := ZipApp(t.TempDir()); err == nil {
		t.Fatal("expected error without a Dockerfile")
	}
}
