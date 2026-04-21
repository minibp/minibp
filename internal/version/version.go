// Package version provides version information for minibp.
// Version information is set at build time using -ldflags.
package version

import (
	"fmt"
	"runtime"
)

// Info contains version information.
type Info struct {
	GitTag       string `json:"gitTag"`
	GitBranch    string `json:"gitBranch"`
	GitCommit    string `json:"gitCommit"`
	GitTreeState string `json:"gitTreeState"`
	BuildDate    string `json:"buildDate"`
	MinibpVer    string `json:"minibpVersion"`
	GoVersion    string `json:"goVersion"`
	Compiler     string `json:"compiler"`
	Platform     string `json:"platform"`
}

// String returns the git tag as the string representation.
func (info Info) String() string {
	return info.GitTag
}

// Get returns the version Info.
// Build-time variables should be set via -ldflags.
func Get() Info {
	return Info{
		GitTag:       gitTag,
		GitBranch:    gitBranch,
		GitCommit:    gitCommit,
		GitTreeState: gitTreeState,
		BuildDate:    buildDate,
		MinibpVer:    minibpVer,
		GoVersion:    runtime.Version(),
		Compiler:     runtime.Compiler,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// Default version information - can be overridden at build time.
var (
	gitTag       = "unknown"
	gitBranch    = "unknown"
	gitCommit    = "unknown"
	gitTreeState = "unknown"
	buildDate    = "unknown"
	minibpVer    = "0.001"
)

// Helper functions to access version info.
// (Kept for potential future use with build-time overrides)
