// Package version says which build of agora runs: the version and the commit it was built from.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// pseudoRE matches a pseudo-version, which the Go toolchain gives a build from a checkout that is
// not at a tag: v0.0.0-20261011154256-1547871db02d.
var pseudoRE = regexp.MustCompile(`-(\d+\.)?\d{14}-[0-9a-f]{12}$`)

// Version is the release version, given at build time with
// -ldflags "-X github.com/vsem-azamat/agora/internal/version.Version=v1.2.3".
var Version string

const (
	// Dev is the version of a build that has none: built from a checkout.
	Dev = "dev"
	// Unknown is the commit of a build the toolchain recorded none for.
	Unknown = "unknown"
	// commitLen is how many characters of the revision name the commit.
	commitLen = 12
)

// Info is a build's version and commit.
type Info struct {
	Version string
	Commit  string // a short revision, with "-dirty" for a tree with changes, or Unknown
}

// String is `<version> (<commit>)`.
func (i Info) String() string { return i.Version + " (" + i.Commit + ")" }

// Get returns the running binary's version and commit.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		bi = nil
	}
	return from(Version, bi)
}

// from reads the version and commit from the version given at build time and the build info.
func from(given string, bi *debug.BuildInfo) Info {
	i := Info{Version: given, Commit: Unknown}
	if bi == nil {
		if i.Version == "" {
			i.Version = Dev
		}
		return i
	}
	// a checkout at a tag gets the tag, perhaps with +dirty, which the commit says already
	if v := strings.TrimSuffix(bi.Main.Version, "+dirty"); i.Version == "" && v != "" && v != "(devel)" && !pseudoRE.MatchString(v) {
		i.Version = v
	}
	if i.Version == "" {
		i.Version = Dev
	}
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				i.Commit = s.Value[:min(len(s.Value), commitLen)]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && i.Commit != Unknown {
		i.Commit += "-dirty"
	}
	return i
}
