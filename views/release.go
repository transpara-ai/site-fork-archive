package views

import "github.com/transpara-ai/site/internal/buildinfo"

func siteVersion() string {
	return buildinfo.Version()
}

func siteReleaseDisplay() string {
	return buildinfo.Display()
}
