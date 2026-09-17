package graph

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/transpara-ai/site/profile"
)

func TestOperationalPagesShowSiteVersionAndRevision(t *testing.T) {
	t.Setenv("SITE_REVISION", "abcdef0123456789")
	var body bytes.Buffer
	data := ConsolePageData{Title: "Config", Active: "config"}
	if err := ConsolePage(data, ViewUser{}, profile.Default()).Render(context.Background(), &body); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-site-version="0.1.0"`, "Site v0.1.0 · abcdef012345"} {
		if !strings.Contains(body.String(), want) {
			t.Fatalf("console page missing release identity %q", want)
		}
	}
}
