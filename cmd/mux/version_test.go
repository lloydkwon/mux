package main

import (
	"runtime/debug"
	"testing"
)

func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main:     debug.Module{Version: mainVersion},
		Settings: settings,
	}
}

func TestResolveVersion(t *testing.T) {
	const sha = "7eec4b92b03fe6a14a3b08e9a69ff8d53f845b38"

	tests := []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{
			name:     "injected tag wins over everything",
			injected: "0.2.0",
			info:     buildInfo("v9.9.9"),
			ok:       true,
			want:     "0.2.0",
		},
		{
			// The Makefile injects `git describe`, which keeps the "v".
			name:     "injected value is normalized like the rest",
			injected: "v0.2.0-dirty",
			info:     nil,
			ok:       false,
			want:     "0.2.0-dirty",
		},
		{
			// Go >= 1.24 derives this from the nearest tag on a local build.
			name:     "local build at a tag with uncommitted changes",
			injected: defaultVersion,
			info:     buildInfo("v0.2.0+dirty"),
			ok:       true,
			want:     "0.2.0+dirty",
		},
		{
			name:     "module version from go install, v prefix trimmed",
			injected: defaultVersion,
			info:     buildInfo("v0.2.0"),
			ok:       true,
			want:     "0.2.0",
		},
		{
			name:     "pseudo-version from an untagged go install",
			injected: defaultVersion,
			info:     buildInfo("v0.0.0-20260809122557-7eec4b92b03f"),
			ok:       true,
			want:     "0.0.0-20260809122557-7eec4b92b03f",
		},
		{
			name:     "local build falls through to the revision",
			injected: defaultVersion,
			info: buildInfo("(devel)",
				debug.BuildSetting{Key: "vcs.revision", Value: sha},
				debug.BuildSetting{Key: "vcs.modified", Value: "false"},
			),
			ok:   true,
			want: "7eec4b92b03f",
		},
		{
			name:     "dirty work tree is marked",
			injected: defaultVersion,
			info: buildInfo("(devel)",
				debug.BuildSetting{Key: "vcs.revision", Value: sha},
				debug.BuildSetting{Key: "vcs.modified", Value: "true"},
			),
			ok:   true,
			want: "7eec4b92b03f-dirty",
		},
		{
			name:     "empty main version is treated like devel",
			injected: defaultVersion,
			info: buildInfo("",
				debug.BuildSetting{Key: "vcs.revision", Value: sha},
			),
			ok:   true,
			want: "7eec4b92b03f",
		},
		{
			name:     "no vcs stamp and no module version",
			injected: defaultVersion,
			info:     buildInfo("(devel)"),
			ok:       true,
			want:     defaultVersion,
		},
		{
			name:     "build info unavailable",
			injected: defaultVersion,
			info:     nil,
			ok:       false,
			want:     defaultVersion,
		},
		{
			name:     "empty injected value is not mistaken for a version",
			injected: "",
			info:     buildInfo("v0.2.0"),
			ok:       true,
			want:     "0.2.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.injected, tt.info, tt.ok); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
