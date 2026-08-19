package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func reset(t *testing.T, v, c, d string) {
	t.Helper()
	origV, origC, origD := Version, Commit, Date
	Version, Commit, Date = v, c, d
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })
}

func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main:     debug.Module{Version: mainVersion},
		Settings: settings,
	}
}

func TestApplyBuildInfo_FillsDefaultsFromModuleVersion(t *testing.T) {
	reset(t, devVersion, devCommit, devDate)

	applyBuildInfo(buildInfo("v1.0.1"))

	assert.Equal(t, "1.0.1", Version, "leading v should be stripped to match ldflags format")
	assert.Equal(t, devCommit, Commit)
	assert.Equal(t, devDate, Date)
}

func TestApplyBuildInfo_FillsDefaultsFromVCSSettings(t *testing.T) {
	reset(t, devVersion, devCommit, devDate)

	applyBuildInfo(buildInfo("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "60e15d8ff5c56d6be0d3a7dd4e172bf3b9046d30"},
		debug.BuildSetting{Key: "vcs.time", Value: "2026-08-19T14:48:04Z"},
		debug.BuildSetting{Key: "vcs.modified", Value: "false"},
	))

	assert.Equal(t, devVersion, Version, "(devel) carries no useful version")
	assert.Equal(t, "60e15d8", Commit)
	assert.Equal(t, "2026-08-19T14:48:04Z", Date)
}

func TestApplyBuildInfo_MarksDirtyTree(t *testing.T) {
	reset(t, devVersion, devCommit, devDate)

	applyBuildInfo(buildInfo("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "60e15d8ff5c56d6be0d3a7dd4e172bf3b9046d30"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	))

	assert.Equal(t, "60e15d8-dirty", Commit)
}

func TestApplyBuildInfo_LinkTimeValuesWin(t *testing.T) {
	reset(t, "1.0.1", "abc1234", "2026-08-19T12:00:00Z")

	applyBuildInfo(buildInfo("v9.9.9",
		debug.BuildSetting{Key: "vcs.revision", Value: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		debug.BuildSetting{Key: "vcs.time", Value: "2000-01-01T00:00:00Z"},
	))

	assert.Equal(t, "1.0.1", Version)
	assert.Equal(t, "abc1234", Commit)
	assert.Equal(t, "2026-08-19T12:00:00Z", Date)
}

func TestApplyBuildInfo_EmptyBuildInfoLeavesDefaults(t *testing.T) {
	reset(t, devVersion, devCommit, devDate)

	applyBuildInfo(buildInfo(""))

	assert.Equal(t, devVersion, Version)
	assert.Equal(t, devCommit, Commit)
	assert.Equal(t, devDate, Date)
}

func TestString(t *testing.T) {
	reset(t, "1.0.1", "60e15d8", "2026-08-19T14:48:04Z")

	assert.Equal(t, "tdm 1.0.1 (60e15d8) built 2026-08-19T14:48:04Z", String())
}

func TestString_OmitsUnresolvedParts(t *testing.T) {
	reset(t, "1.0.2", devCommit, devDate)

	assert.Equal(t, "tdm 1.0.2", String(), "proxy builds carry no VCS stamps")
}

func TestString_OmitsDateOnly(t *testing.T) {
	reset(t, "1.0.2", "60e15d8", devDate)

	assert.Equal(t, "tdm 1.0.2 (60e15d8)", String())
}
