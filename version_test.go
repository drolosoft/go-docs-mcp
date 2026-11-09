package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionFrom(t *testing.T) {
	cases := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "dev"},
		{"local build", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, "dev"},
		{"empty", &debug.BuildInfo{Main: debug.Module{Version: ""}}, true, "dev"},
		{"go install @v1.2.0", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.0"}}, true, "1.2.0"},
		{"pseudo-version", &debug.BuildInfo{Main: debug.Module{Version: "v1.1.1-0.20260817120000-abcdef123456"}}, true, "1.1.1-0.20260817120000-abcdef123456"},
	}
	for _, c := range cases {
		if got := versionFrom(c.info, c.ok); got != c.want {
			t.Errorf("%s: versionFrom = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	// The build info a `go install @v1.4.1` binary carries, to check that a
	// link-time version still wins over it.
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v1.4.1"}}

	// The build info of the Docker build stage, which has no git.
	local := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}

	cases := []struct {
		name   string
		linked string
		info   *debug.BuildInfo
		ok     bool
		want   string
	}{
		{"image built with VERSION", "1.5.0", local, true, "1.5.0"},
		{"linked with a leading v", "v1.5.0", local, true, "1.5.0"},
		{"linked wins over build info", "1.5.0", installed, true, "1.5.0"},
		{"linked with spaces", " 1.5.0\n", local, true, "1.5.0"},
		{"image built without VERSION", "", local, true, "dev"},
		{"blank linked falls back", "   ", installed, true, "1.4.1"},
		{"go install, nothing linked", "", installed, true, "1.4.1"},
		{"no build info, nothing linked", "", nil, false, "dev"},
	}
	for _, c := range cases {
		if got := resolveVersion(c.linked, c.info, c.ok); got != c.want {
			t.Errorf("%s: resolveVersion = %q, want %q", c.name, got, c.want)
		}
	}
}
