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

func TestEmbeddedRevisionIsUsedWithoutRuntimeOverride(t *testing.T) {
	t.Setenv("SITE_REVISION", "")
	previous := embeddedRevision
	embeddedRevision = "fedcba9876543210"
	t.Cleanup(func() { embeddedRevision = previous })
	if got, want := Revision(), "fedcba987654"; got != want {
		t.Fatalf("Revision() = %q, want embedded %q", got, want)
	}
}
