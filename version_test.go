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
