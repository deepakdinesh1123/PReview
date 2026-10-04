package cloud

import "testing"

func TestParseS3URI(t *testing.T) {
	cases := []struct {
		in          string
		bucket, key string
		wantErr     bool
	}{
		{"s3://bucket", "bucket", "", false},
		{"s3://bucket/", "bucket", "", false},
		{"s3://bucket/a/b/", "bucket", "a/b", false},
		{"https://bucket/a", "", "", true},
		{"s3:///a", "", "", true},
	}
	for _, c := range cases {
		b, k, err := ParseS3URI(c.in)
		if (err != nil) != c.wantErr || b != c.bucket || k != c.key {
			t.Errorf("%q: got (%q,%q,%v)", c.in, b, k, err)
		}
	}
}

func TestObjectKeyNeverHasLeadingSlash(t *testing.T) {
	if got := objectKey("", "name/file.zip"); got != "name/file.zip" {
		t.Fatalf("empty prefix: %q", got)
	}
	if got := objectKey("pre/fix", "n/f.zip"); got != "pre/fix/n/f.zip" {
		t.Fatalf("with prefix: %q", got)
	}
}

func TestIsLive(t *testing.T) {
	for state, want := range map[string]bool{
		"PENDING": true, "RUNNING": true, "SUSPENDING": true, "SUSPENDED": true,
		"TERMINATING": false, "TERMINATED": false, "": false,
	} {
		if IsLive(state) != want {
			t.Errorf("IsLive(%q) != %v", state, want)
		}
	}
}
