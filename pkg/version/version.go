package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// Default values, overridden at link time by Makefile, Dockerfile, and
// GoReleaser via -X flags. When those are absent — most notably for binaries
// produced by "go install <module>/cmd/tdm@<tag>" — they are filled in from the
// build information the toolchain embeds instead.
const (
	devVersion = "dev"
	devCommit  = "none"
	devDate    = "unknown"
)

var (
	Version = devVersion
	Commit  = devCommit
	Date    = devDate
)

func init() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	applyBuildInfo(bi)
}

// applyBuildInfo fills any field still holding its default with the equivalent
// value from bi. Link-time values always win, so a stripped release binary is
// never overwritten by the module version recorded in the build info.
func applyBuildInfo(bi *debug.BuildInfo) {
	if Version == devVersion {
		// "(devel)" is what the toolchain reports for a build that is not tied
		// to a module tag; it carries less information than "dev", so skip it.
		if v := strings.TrimPrefix(bi.Main.Version, "v"); v != "" && v != "(devel)" {
			Version = v
		}
	}

	var revision, vcsTime string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}

	if Commit == devCommit && revision != "" {
		if len(revision) > 7 {
			revision = revision[:7]
		}
		if modified {
			revision += "-dirty"
		}
		Commit = revision
	}

	if Date == devDate && vcsTime != "" {
		Date = vcsTime
	}
}

// String returns a formatted version string.
func String() string {
	return fmt.Sprintf("tdm %s (%s) built %s", Version, Commit, Date)
}
