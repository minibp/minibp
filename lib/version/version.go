// Package version provides version information for minibp.
// This package encapsulates all build-time and runtime version metadata,
// allowing the binary to self-report its version without external configuration files.
//
// Version information is injected at build time using Go's -ldflags mechanism
// via the -X flag, which sets package-level variables. This approach ensures
// version information is embedded directly in the binary, making it portable
// and not requiring runtime file access.
//
// Example build invocation with version injection:
//
//	go build -ldflags=" \
//		-X github.com/anomalyco/minibp/lib/version.gitTag=v1.2.3 \
//		-X github.com/anomalyco/minibp/lib/version.gitBranch=main \
//		-X github.com/anomalyco/minibp/lib/version.gitCommit=$(git rev-parse HEAD) \
//		-X github.com/anomalyco/minibp/lib/version.gitTreeState=$(git status --porcelain | wc -l | xargs test 0 -eq 1 && echo dirty || echo clean) \
//		-X github.com/anomalyco/minibp/lib/version.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
//		-X github.com/anomalyco/minibp/lib/version.minibpVer=0.001" \
//		./cmd/minibp
//
// The Get() function returns version info combining both build-time injected values
// (git metadata, build date) and runtime-detected values (Go version, compiler, platform).
package version

import (
	"fmt"
	"runtime"
)

// Info contains comprehensive version information about the minibp build.
// All fields are exported to support JSON serialization for programmatic access,
// such as in CI/CD pipelines or version reporting tools.
//
// This struct is designed to be backwards compatible: missing build-time
// injection values default to "unknown" rather than empty strings.
type Info struct {
	// GitTag is the Git tag from the most recent commit, e.g., "v1.2.3".
	// This is typically set by git describe --tags at build time.
	// Empty if no tag exists or not injected.
	GitTag string `json:"gitTag"`

	// GitBranch is the current Git branch name, e.g., "main", "feature/my-branch".
	// This is typically set by git branch --show-current at build time.
	GitBranch string `json:"gitBranch"`

	// GitCommit is the full Git commit hash (40 characters), e.g., "abc123def456...".
	// This is set by git rev-parse HEAD at build time.
	GitCommit string `json:"gitCommit"`

	// GitTreeState describes the state of the Git working tree at build time.
	// Values: "clean" (no uncommitted changes) or "dirty" (uncommitted changes exist).
	// This is determined by checking git status --porcelain at build time.
	GitTreeState string `json:"gitTreeState"`

	// BuildDate is the date and time of the build in ISO 8601 format, e.g., "2024-01-15T10:30:00Z".
	// Uses UTC timezone to ensure consistent cross-timezone builds.
	// Format: YYYY-MM-DDTHH:MM:SSZ
	BuildDate string `json:"buildDate"`

	// MinibpVer is the semantic version of minibp itself, e.g., "0.001".
	// This differs from GitTag as it tracks the project's own versioning scheme.
	// Defaults to "0.001" before the first official release.
	MinibpVer string `json:"minibpVersion"`

	// GoVersion is the Go runtime version, e.g., "go1.21.0".
	// Detected at runtime via runtime.Version().
	GoVersion string `json:"goVersion"`

	// Compiler is the Go compiler used, either "gc" (standard compiler) or "gccgo".
	// Detected at runtime via runtime.Compiler.
	Compiler string `json:"compiler"`

	// Platform is the target platform in OS/arch format, e.g., "linux/amd64", "darwin/arm64".
	// Detected at runtime via runtime.GOOS and runtime.GOARCH.
	Platform string `json:"platform"`
}

// String returns the Git tag as the string representation of the version info.
// This implements the fmt.Stringer interface for convenient printing.
//
// Returns:
//   - The GitTag value if non-empty and not "unknown"
//   - "unknown" if the tag was not set at build time
//
// Example output: "v1.2.3" or "unknown"
func (info Info) String() string {
	return info.GitTag
}

// Get returns the complete version Info for this binary.
//
// This function combines build-time injected values (from -ldflags) with
// runtime-detected values. Build-time injection ensures version consistency
// across deployments, while runtime detection provides accurate environment info.
//
// Build-time injected values (may be "unknown" if not set):
//   - gitTag, gitBranch, gitCommit, gitTreeState, buildDate, minibpVer
//
// Runtime-detected values (always accurate for the current execution):
//   - GoVersion, Compiler, Platform
//
// Returns:
//   - Info struct populated with all version fields
//
// Note: The returned struct shares no pointers with internal state,
// making it safe to modify without affecting future Get() calls.
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

// Package-level variables for build-time version injection.
// These are marked as private to prevent direct access from other packages,
// enforcing the use of the Get() function which provides the complete Info struct.
//
// Build tools inject values using the -X flag with the full import path:
//
//	-X github.com/anomalyco/minibp/lib/version.gitTag=<tag>
//	-X github.com/anomalyco/minibp/lib/version.gitBranch=<branch>
//	- -X github.com/anomalyco/minibp/lib/version.gitCommit=<commit>
//	- -X github.com/anomalyco/minibp/lib/version.gitTreeState=<state>
//	- -X github.com/anomalyco/minibp/lib/version.buildDate=<date>
//	- -X github.com/anomalyco/minibp/lib/version.minibpVer=<version>
//
// Default values ensure the binary remains functional even without injection,
// though version reporting will show "unknown" for missing fields.
var (
	// gitTag is the Git tag from the most recent commit.
	// Set by: git describe --tags --abbrev=0
	gitTag = "unknown"

	// gitBranch is the current Git branch name.
	// Set by: git branch --show-current
	gitBranch = "unknown"

	// gitCommit is the full Git commit hash (40 hex characters).
	// Set by: git rev-parse HEAD
	gitCommit = "unknown"

	// gitTreeState indicates whether the working tree had uncommitted changes.
	// Set by: git status --porcelain | wc -l | xargs test 0 -eq 1 && echo dirty || echo clean
	gitTreeState = "unknown"

	// buildDate is the build timestamp in ISO 8601 format (UTC).
	// Set by: date -u +%Y-%m-%dT%H:%M:%SZ
	buildDate = "unknown"

	// minibpVer is the project's semantic version string.
	// Starts at "0.001" before official release tagging begins.
	minibpVer = "0.001"
)
