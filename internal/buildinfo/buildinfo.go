// Package buildinfo exposes the Site release identity shown on rendered pages.
package buildinfo

import (
	_ "embed"
	"os"
	"strings"
)

//go:embed VERSION
var version string

// Version returns the semantic release version embedded in the Site binary.
func Version() string {
	return strings.TrimSpace(version)
}

// Revision returns the short source revision injected by the deployment.
func Revision() string {
	revision := strings.TrimSpace(os.Getenv("SITE_REVISION"))
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

// Display returns a compact operator-facing release identity.
func Display() string {
	label := "v" + Version()
	if revision := Revision(); revision != "" {
		return label + " · " + revision
	}
	return label
}
