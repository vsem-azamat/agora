// Package version says which build of agora runs: the version and the commit it was built from.
package version

import (
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
)

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
	bi, _ := debug.ReadBuildInfo() // nil when the binary has none
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
	// a checkout at a tag gets the tag, perhaps with +dirty, which the commit says already; a
	// checkout between tags, and go install of a commit, get a pseudo-version, which names the commit
	recorded := strings.TrimSuffix(bi.Main.Version, "+dirty")
	pseudo := module.IsPseudoVersion(recorded)
	if i.Version == "" && recorded != "" && recorded != "(devel)" && !pseudo {
		i.Version = recorded
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
	if i.Commit == Unknown && pseudo {
		if rev, err := module.PseudoVersionRev(recorded); err == nil {
			i.Commit = rev[:min(len(rev), commitLen)]
		}
	}
	if dirty && i.Commit != Unknown {
		i.Commit += "-dirty"
	}
	return i
}
