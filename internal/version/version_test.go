package version

import (
	"runtime/debug"
	"testing"
)

func TestInfoFromTheBuild(t *testing.T) {
	settings := func(rev, modified string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified}}
	}
	for _, c := range []struct {
		name  string
		given string
		bi    *debug.BuildInfo
		want  string
	}{
		{"checkout", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: settings("3bc332799d4c0ad4f958483fc6541f2066bc87ad", "false")}, "dev (3bc332799d4c)"},
		{"release", "v0.4.0", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.9"}, Settings: settings("3bc332799d4c0ad4f958483fc6541f2066bc87ad", "false")}, "v0.4.0 (3bc332799d4c)"},
		{"go install", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.1"}}, "v0.3.1 (unknown)"},
		{"changes", "", &debug.BuildInfo{Settings: settings("3bc332799d4c0ad4f958483fc6541f2066bc87ad", "true")}, "dev (3bc332799d4c-dirty)"},
		{"short revision", "", &debug.BuildInfo{Settings: settings("abc", "false")}, "dev (abc)"},
		{"no build info", "", nil, "dev (unknown)"},
		{"pseudo-version", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261011154256-1547871db02d"}, Settings: settings("1547871db02d0000", "false")}, "dev (1547871db02d)"},
		{"pseudo-version after a tag", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.4.1-0.20261011154256-1547871db02d+dirty"}, Settings: settings("1547871db02d0000", "true")}, "dev (1547871db02d-dirty)"},
		{"pseudo-version after a pre-release", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.5.0-rc.1.0.20261011154739-d77a58cc1e45"}, Settings: settings("d77a58cc1e450000", "false")}, "dev (d77a58cc1e45)"},
		{"go install of a commit", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261011154452-358e4fddb488"}}, "dev (358e4fddb488)"},
		{"at a tag with changes", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.4.0+dirty"}, Settings: settings("3bc332799d4c0000", "true")}, "v0.4.0 (3bc332799d4c-dirty)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := from(c.given, c.bi).String(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
