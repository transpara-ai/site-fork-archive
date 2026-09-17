package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/transpara-ai/site/profile"
)

func TestSharedLayoutShowsSiteVersion(t *testing.T) {
	t.Setenv("SITE_REVISION", "abcdef0123456789")
	var body bytes.Buffer
	if err := Layout("Test", "", profile.Default()).Render(context.Background(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `data-site-version="0.1.0"`) || !strings.Contains(body.String(), "Site v0.1.0 · abcdef012345") {
		t.Fatalf("shared layout missing release identity")
	}
}
