package buildinfo

import (
	"regexp"
	"testing"
)

func TestVersionIsSemverAndDisplayIncludesRevision(t *testing.T) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(Version()) {
		t.Fatalf("Version() = %q, want semantic version", Version())
	}
	t.Setenv("SITE_REVISION", "0123456789abcdef")
	if got, want := Display(), "v"+Version()+" · 0123456789ab"; got != want {
		t.Fatalf("Display() = %q, want %q", got, want)
	}
}
