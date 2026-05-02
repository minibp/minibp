// Package version provides version information for minibp.
// This package encapsulates all build-time and runtime version metadata,
// allowing the binary to self-report its version without external configuration files.
//
// Version information is injected at build time using Go's -ldflags mechanism
// via the -X flag, which sets package-level variables. This approach ensures
// version information is embedded directly in the binary, making it portable
// and not requiring runtime file access.
//
// The Get() function returns version info combining both build-time injected values
// (git metadata, build date) and runtime-detected values (Go version, compiler, platform).
//
// Typical build command with version injection:
//
//	go build -ldflags=" \
//	  -X 'minibp/lib/version.gitTag=$(git describe --tags --abbrev=0)' \
//	  -X 'minibp/lib/version.gitBranch=$(git branch --show-current)' \
//	  -X 'minibp/lib/version.gitCommit=$(git rev-parse HEAD)' \
//	  -X 'minibp/lib/version.gitTreeState=$(test -z "$(git status --porcelain)" && echo clean || echo dirty)' \
//	  -X 'minibp/lib/version.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)' \
//	  -X 'minibp/lib/version.minibpVer=0.001'" \
//	  -o minibp cmd/minibp/main.go
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
//
// JSON serialization example:
//
//	data, _ := json.Marshal(version.Get())
//	fmt.Println(string(data))
//
// Fields are grouped by source:
//   - Build-time: GitTag, GitBranch, GitCommit, GitTreeState, BuildDate, MinibpVer
//   - Runtime: GoVersion, Compiler, Platform
type Info struct {
	GitTag       string `json:"gitTag"`        // Git tag from most recent commit (e.g., "v1.2.3"), set by git describe --tags at build time
	GitBranch    string `json:"gitBranch"`     // Current Git branch name (e.g., "main"), set by git branch --show-current at build time
	GitCommit    string `json:"gitCommit"`     // Full Git commit hash (40 chars), set by git rev-parse HEAD at build time
	GitTreeState string `json:"gitTreeState"`  // Working tree state: "clean" (no changes) or "dirty" (uncommitted changes exist)
	BuildDate    string `json:"buildDate"`     // Build timestamp in ISO 8601 format (e.g., "2024-01-15T10:30:00Z"), uses UTC timezone
	MinibpVer    string `json:"minibpVersion"` // Project semantic version (e.g., "0.001"), differs from GitTag as it tracks project's own versioning
	GoVersion    string `json:"goVersion"`     // Go runtime version (e.g., "go1.21.0"), detected at runtime via runtime.Version()
	Compiler     string `json:"compiler"`      // Go compiler used: "gc" (standard) or "gccgo", detected at runtime via runtime.Compiler
	Platform     string `json:"platform"`      // Target platform in OS/arch format (e.g., "linux/amd64"), detected via runtime.GOOS/GOARCH
}

// String returns the Git tag as the string representation of the version info.
// This implements the fmt.Stringer interface for convenient printing,
// allowing direct use in fmt.Printf with %s or fmt.Sprintf.
//
// Parameters:
//   - info: The Info struct instance (receiver)
//
// Returns:
//   - The GitTag value if non-empty
//   - Empty string if GitTag is not set
//
// Edge cases:
//   - Empty GitTag returns empty string (not "unknown") since String() accesses the field directly
//   - "unknown" GitTag returns "unknown" string
//
// Notes:
//   - This method returns only the GitTag, not the full version info
//   - For complete version output, use Get() and access desired fields directly
//
// Example output: "v1.2.3" or "unknown" or ""
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
//   - gitTag: Git tag from most recent commit, e.g., "v1.2.3"
//   - gitBranch: Current branch name, e.g., "main"
//   - gitCommit: Full commit hash, e.g., "abc123..."
//   - gitTreeState: "clean" or "dirty" indicating uncommitted changes
//   - buildDate: ISO 8601 timestamp of build, e.g., "2024-01-15T10:30:00Z"
//   - minibpVer: Project version, e.g., "0.001"
//
// Runtime-detected values (always accurate for the current execution):
//   - GoVersion: Go runtime version, e.g., "go1.21.0"
//   - Compiler: Go compiler used, "gc" or "gccgo"
//   - Platform: OS/arch tuple, e.g., "linux/amd64"
//
// Parameters: none
//
// Returns:
//   - Info struct populated with all version fields
//
// Edge cases:
//   - If build-time injection failed, fields will have default "unknown" values
//   - Runtime values are always populated from the running process
//   - Platform format uses runtime.GOOS/runtime.GOARCH, not build target
//
// Notes:
//   - The returned struct shares no pointers with internal state
//   - Safe to modify without affecting future Get() calls
//   - The caller may freely modify the returned struct
func Get() Info {
	return Info{
		GitTag:       gitTag,                                             // Build-time injected: Git tag from most recent commit
		GitBranch:    gitBranch,                                          // Build-time injected: current branch name
		GitCommit:    gitCommit,                                          // Build-time injected: full commit hash
		GitTreeState: gitTreeState,                                       // Build-time injected: "clean" or "dirty"
		BuildDate:    buildDate,                                          // Build-time injected: ISO 8601 build timestamp
		MinibpVer:    minibpVer,                                          // Build-time injected: project semantic version
		GoVersion:    runtime.Version(),                                  // Runtime detected: Go runtime version
		Compiler:     runtime.Compiler,                                   // Runtime detected: compiler type ("gc" or "gccgo")
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH), // Runtime detected: OS/arch tuple
	}
}

// Package-level variables for build-time version injection.
// These are marked as private to prevent direct access from other packages,
// enforcing the use of the Get() function which provides the complete Info struct.
//
// Build tools inject values using the -X flag with the full import path.
//
// Default values ensure the binary remains functional even without injection,
// though version reporting will show "unknown" for missing fields.
//
// Example ldflags injection:
//
//	-X 'minibp/lib/version.gitTag=v1.0.0'
//	-X 'minibp/lib/version.gitCommit=$(git rev-parse HEAD)'
//	-X 'minibp/lib/version.gitTreeState=$(test -z "$(git status --porcelain)" && echo clean || echo dirty)'
//	-X 'minibp/lib/version.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)'
//	-X 'minibp/lib/version.minibpVer=0.001'
var (
	gitTag       = "unknown" // Git tag from most recent commit, set by git describe --tags --abbrev=0
	gitBranch    = "unknown" // Current Git branch name (e.g., "main"), set by git branch --show-current
	gitCommit    = "unknown" // Full Git commit hash (40 hex chars), set by git rev-parse HEAD
	gitTreeState = "unknown" // Working tree state: "clean" (no changes) or "dirty" (uncommitted changes exist)
	buildDate    = "unknown" // Build timestamp in ISO 8601 format (UTC), set by date -u +%Y-%m-%dT%H:%M:%SZ
	minibpVer    = "0.001"   // Project semantic version (e.g., "0.001"), tracks project's own versioning scheme
)
