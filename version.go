package main

import (
	"runtime/debug"
	"strings"
)

// linkedVersion is set at link time with -ldflags "-X main.linkedVersion=1.5.0".
// The Docker image needs it: its build stage compiles a copy of the source
// without git, so the build info only says "(devel)". Every other build leaves
// it empty and the version comes from the build info.
var linkedVersion string

// serverVersion is what we report in MCP serverInfo. A version given at link
// time wins. Otherwise it comes from the Go module version baked in by
// `go install github.com/drolosoft/go-docs-mcp@vX.Y.Z` (so it always matches
// the git tag users installed) and falls back to "dev" for local builds, where
// the module version is "(devel)" or empty.
func serverVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(linkedVersion, info, ok)
}

// resolveVersion picks the version to report: the link-time one when it is
// set, the build info one otherwise. A leading "v" is removed from either, so
// the image reports "1.5.0" whether the release passed "1.5.0" or "v1.5.0".
func resolveVersion(linked string, info *debug.BuildInfo, ok bool) string {
	linked = strings.TrimSpace(linked)
	if linked != "" {
		return strings.TrimPrefix(linked, "v")
	}

	return versionFrom(info, ok)
}

// versionFrom turns the result of debug.ReadBuildInfo into the version we
// report, with the leading "v" removed. It takes the build info as arguments
// so the tests can feed it every case without building a binary.
func versionFrom(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "dev"
	}
	v := strings.TrimSpace(info.Main.Version)
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}
