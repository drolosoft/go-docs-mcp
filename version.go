package main

import (
	"runtime/debug"
	"strings"
)

// serverVersion is what we report in MCP serverInfo. It comes from the Go
// module version baked in by `go install github.com/drolosoft/go-docs-mcp@vX.Y.Z`
// (so it always matches the git tag users installed) and falls back to "dev"
// for local builds, where the module version is "(devel)" or empty.
func serverVersion() string {
	return versionFrom(debug.ReadBuildInfo())
}

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
