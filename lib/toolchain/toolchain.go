// Package toolchain provides cross-architecture compilation support.
// It manages toolchain configurations for different target architectures and operating systems.
//
// This package handles:
//   - Detection of available compilers (gcc, g++, ar) based on target platform
//   - Architecture-specific compiler flags (-march, -m32/-m64)
//   - Cross-compilation toolchain prefixes (arm-linux-gnueabihf-, aarch64-linux-gnu-)
//   - Sysroot configuration for embedded builds
//   - Toolchain validation and caching
//
// Example usage:
//
//	tc := NewToolchainConfig()
//	toolchain, err := tc.DetectToolchain(Arm64, Android)
//	flags := toolchain.GetCompileFlags()
package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Architecture represents a target CPU architecture.
//
// Description:
// Supported architectures include:
//   - Arm: 32-bit ARM (ARMv7)
//   - Arm64: 64-bit ARM (ARMv8/AArch64)
//   - X86: 32-bit x86 (i386/i686)
//   - X86_64: 64-bit x86 (amd64)
type Architecture string

const (
	Arm    Architecture = "arm"
	Arm64  Architecture = "arm64"
	X86    Architecture = "x86"
	X86_64 Architecture = "x86_64"
)

// OS represents a target operating system.
//
// Description:
// Supported operating systems include:
//   - Linux: Linux systems
//   - Windows: Windows systems
//   - Darwin: macOS/Darwin systems
//   - Android: Android systems
type OS string

const (
	Linux   OS = "linux"
	Windows OS = "windows"
	Darwin  OS = "darwin"
	Android OS = "android"
)

// Toolchain represents a complete toolchain configuration for a given target architecture and operating system.
//
// Description:
// It contains paths to the C compiler, C++ compiler, static library archiver, and linker,
// as well as optional sysroot configuration.
//
// Fields:
//   - Arch: Target architecture (Arm, Arm64, X86, X86_64)
//   - OS: Target operating system (Linux, Windows, Darwin, Android)
//   - CC: Path to C compiler executable
//   - CXX: Path to C++ compiler executable
//   - AR: Path to static library archiver (ar)
//   - LD: Path to linker executable
//   - Sysroot: Optional sysroot directory for cross-compilation
type Toolchain struct {
	Arch    Architecture // Target CPU architecture
	OS      OS           // Target operating system
	CC      string       // Path to C compiler executable
	CXX     string       // Path to C++ compiler executable
	AR      string       // Path to static library archiver
	LD      string       // Path to linker executable
	Sysroot string       // Optional sysroot directory for cross-compilation
}

// ToolchainConfig provides toolchain detection and configuration management.
//
// Description:
// It maintains a cache of detected toolchains to avoid repeated detection and provides methods to discover
// and configure compilers for different target architectures and operating systems.
//
// How it works:
// The ToolchainConfig holds default tool names and a map of cached toolchain configurations
// keyed by "architecture-os" strings.
//
// Fields:
//   - defaultCC: Default C compiler name (usually "gcc")
//   - defaultCXX: Default C++ compiler name (usually "g++")
//   - defaultAR: Default static library archiver name (usually "ar")
//   - defaultLD: Default linker name (usually "ld")
//   - toolchains: Cache of detected toolchains mapped by key
type ToolchainConfig struct {
	defaultCC  string                // Default C compiler name
	defaultCXX string                // Default C++ compiler name
	defaultAR  string                // Default static library archiver name
	defaultLD  string                // Default linker name
	toolchains map[string]*Toolchain // Cache of detected toolchains
}

// NewToolchainConfig creates a new ToolchainConfig instance.
//
// Description:
// Initializes a ToolchainConfig with default toolchain tool names (gcc, g++, ar, ld) and an empty toolchain cache.
//
// Parameters:
//   - (none) All parameters are provided as defaults
//
// Returns:
//   - *ToolchainConfig: A pointer to a new ToolchainConfig ready for use. The returned instance
//     must be used to detect toolchains via DetectToolchain method.
//
// Note:
// The returned instance starts with an empty cache. Call DetectToolchain to populate
// the cache with toolchain configurations.
func NewToolchainConfig() *ToolchainConfig {
	return &ToolchainConfig{
		defaultCC:  "gcc",
		defaultCXX: "g++",
		defaultAR:  "ar",
		defaultLD:  "ld",
		toolchains: make(map[string]*Toolchain),
	}
}

// DetectToolchain detects or retrieves from cache the appropriate toolchain.
//
// Description:
// If a toolchain for the specified arch/OS combination has already been detected,
// it returns the cached instance. Otherwise, it performs toolchain detection.
//
// How it works:
// Toolchain detection includes:
//  1. Creating a new Toolchain with default tool names
//  2. Attempting to find architecture-specific cross-compiler tools
//  3. Falling back to default tools if specialized tools are not found
//  4. Caching the result for future lookups
//
// Parameters:
//   - arch: Target architecture (Arm, Arm64, X86, X86_64)
//   - targetOS: Target operating system (Linux, Windows, Darwin, Android)
//
// Returns:
//   - *Toolchain: The detected toolchain configuration
//   - error: Any error that occurred during detection (nil if successful)
//
// Edge cases:
//   - Cached toolchain is returned immediately if available (fast path)
//   - Cross-compiler not found falls back to default tool names
//   - Empty cache results in fresh detection with caching
func (tc *ToolchainConfig) DetectToolchain(arch Architecture, targetOS OS) (*Toolchain, error) {
	key := fmt.Sprintf("%s-%s", arch, targetOS)

	if toolchain, ok := tc.toolchains[key]; ok {
		return toolchain, nil
	}

	toolchain := &Toolchain{
		Arch: arch,
		OS:   targetOS,
		CC:   tc.defaultCC,
		CXX:  tc.defaultCXX,
		AR:   tc.defaultAR,
		LD:   tc.defaultLD,
	}

	toolchain.CC, toolchain.CXX, toolchain.AR = tc.detectTools(arch, targetOS)

	tc.toolchains[key] = toolchain
	return toolchain, nil
}

// detectTools detects the appropriate compiler tools for the given target.
//
// Description:
// Detects the C compiler, C++ compiler, and archiver for the target architecture and OS.
//
// How it works:
// The detection logic follows these steps:
//  1. Start with default tool names (gcc, g++, ar)
//  2. Check for architecture-specific toolchain prefix
//  3. If a prefix is found (e.9., "arm-linux-gnueabihf"), try using
//     prefix-gcc, prefix-g++, prefix-ar tool names
//  4. Verify each tool exists in PATH; if not found, fall back to default
//
// Parameters:
//   - arch: Target architecture
//   - targetOS: Target operating system
//
// Returns:
//   - cc: Path to C compiler
//   - cxx: Path to C++ compiler
//   - ar: Path to static archiver
//
// Edge cases:
//   - Cross-compiler tools not found: falls back to default gcc/g++
//   - Empty prefix: uses default tool names directly
//   - Tool not in PATH: falls back to default for that tool only
func (tc *ToolchainConfig) detectTools(arch Architecture, targetOS OS) (cc, cxx, ar string) {
	cc = tc.defaultCC
	cxx = tc.defaultCXX
	ar = tc.defaultAR

	prefix := tc.getToolchainPrefix(arch, targetOS)
	if prefix != "" {
		cc = prefix + "-gcc"
		cxx = prefix + "-g++"
		ar = prefix + "-ar"
	}

	if !tc.toolExists(cc) {
		cc = tc.defaultCC
	}
	if !tc.toolExists(cxx) {
		cxx = tc.defaultCXX
	}
	if !tc.toolExists(ar) {
		ar = tc.defaultAR
	}

	return cc, cxx, ar
}

// getToolchainPrefix returns the toolchain prefix for cross-compilation.
//
// Description:
// Returns the toolchain prefix string used for cross-compilation tool discovery.
// The prefix is combined with standard tool names (gcc, g++, ar) to form cross-compiler
// tool names like "arm-linux-gnueabihf-gcc".
//
// How it works:
// The mapping is specific to the target OS and architecture:
//   - Android: arm-linux-androideabi, aarch64-linux-android,
//     i686-linux-android, x86_64-linux-android
//   - Linux: arm-linux-gnueabihf, aarch64-linux-gnu,
//     i686-linux-gnu, x86_64-linux-gnu
//
// Parameters:
//   - arch: Target architecture
//   - targetOS: Target operating system
//
// Returns:
//   - string: Toolchain prefix (empty string if no prefix applies)
//
// Edge cases:
//   - Windows/Darwin targets: returns empty (no cross-compiler mapping)
//   - Unknown architecture: returns empty
func (tc *ToolchainConfig) getToolchainPrefix(arch Architecture, targetOS OS) string {
	switch targetOS {
	case Android:
		switch arch {
		case Arm:
			return "arm-linux-androideabi"
		case Arm64:
			return "aarch64-linux-android"
		case X86:
			return "i686-linux-android"
		case X86_64:
			return "x86_64-linux-android"
		}
	case Linux:
		switch arch {
		case Arm:
			return "arm-linux-gnueabihf"
		case Arm64:
			return "aarch64-linux-gnu"
		case X86:
			return "i686-linux-gnu"
		case X86_64:
			return "x86_64-linux-gnu"
		}
	}
	return ""
}

// toolExists checks if a tool exists in the system PATH.
//
// Description:
// It uses findExecutable to search for the tool and returns true if the executable is found,
// false otherwise.
//
// Parameters:
//   - name: Name or path of the tool to check
//
// Returns:
//   - bool: True if the tool exists and is executable, false otherwise
//
// Note:
// This is a convenience method that wraps findExecutable.
func (tc *ToolchainConfig) toolExists(name string) bool {
	_, err := tc.findExecutable(name)
	return err == nil
}

// findExecutable searches for an executable in the system PATH.
//
// Description:
// This is a wrapper around execLookup that allows for dependency injection in tests.
//
// Parameters:
//   - name: Name or path of the executable to find
//
// Returns:
//   - string: Full path to the executable if found
//   - error: Error if not found
//
// Note:
// This method exists to allow dependency injection in tests.
func (tc *ToolchainConfig) findExecutable(name string) (string, error) {
	return execLookup(name)
}

// execLookup searches for an executable by name in all directories listed in the system PATH environment variable.
//
// Description:
// It checks each directory by joining the directory path with the executable name and testing if the file
// exists and is not a directory.
//
// Parameters:
//   - name: The name of the executable to find
//
// Returns:
//   - string: Full path to the executable if found
//   - error: Error if the executable is not found in any PATH directory
//
// Edge cases:
//   - Empty PATH environment variable: returns not found error
//   - Executable in current directory with "./" prefix: handled correctly
//   - PATH directories that don't exist: skipped silently
func execLookup(name string) (string, error) {
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		execPath := filepath.Join(dir, name)
		if info, err := os.Stat(execPath); err == nil && !info.IsDir() {
			return execPath, nil
		}
	}
	return "", fmt.Errorf("executable not found: %s", name)
}

// GetCompileFlags returns architecture-specific compilation flags.
//
// Description:
// Returns flags typically passed to the C/C++ compiler to generate code for the target architecture.
//
// How it works:
// The returned flags include:
//   - -march flag for ARM architectures (armv7-a, armv8-a)
//   - -m32 or -m64 flag for x86 architectures
//   - --sysroot flag if Sysroot is set in the Toolchain
//
// Parameters:
//   - (none)
//
// Returns:
//   - []string: Slice of compiler flag strings
//
// Edge cases:
//   - Empty sysroot: no --sysroot flag added
//   - Unknown architecture: no architecture-specific flags added
func (t *Toolchain) GetCompileFlags() []string {
	flags := []string{}

	switch t.Arch {
	case Arm:
		flags = append(flags, "-march=armv7-a", "-mthumb")
	case Arm64:
		flags = append(flags, "-march=armv8-a")
	case X86:
		flags = append(flags, "-m32")
	case X86_64:
		flags = append(flags, "-m64")
	}

	if t.Sysroot != "" {
		flags = append(flags, "--sysroot="+t.Sysroot)
	}

	return flags
}

// GetLinkFlags returns architecture-specific linker flags.
//
// Description:
// Returns flags typically passed to the linker to generate executables or shared libraries
// for the target architecture.
//
// How it works:
// The returned flags include:
//   - -march flag for ARM architectures (armv7-a, armv8-a)
//   - -m32 or -m64 flag for x86 architectures
//   - --sysroot flag if Sysroot is set in the Toolchain
//
// Parameters:
//   - (none)
//
// Returns:
//   - []string: Slice of linker flag strings
//
// Edge cases:
//   - Empty sysroot: no --sysroot flag added
//   - Unknown architecture: no architecture-specific flags added
func (t *Toolchain) GetLinkFlags() []string {
	flags := []string{}

	switch t.Arch {
	case Arm:
		flags = append(flags, "-march=armv7-a")
	case Arm64:
		flags = append(flags, "-march=armv8-a")
	case X86:
		flags = append(flags, "-m32")
	case X86_64:
		flags = append(flags, "-m64")
	}

	if t.Sysroot != "" {
		flags = append(flags, "--sysroot="+t.Sysroot)
	}

	return flags
}

// GetOutputPrefix returns a prefix string for output file naming.
//
// Description:
// Returns a prefix formed by concatenating the architecture and operating system names.
// This is useful for generating unique output file names when building for multiple target configurations.
//
// Example: "arm64-android", "x86_64-linux"
//
// Parameters:
//   - (none)
//
// Returns:
//   - string: Prefix string in format "architecture-os"
func (t *Toolchain) GetOutputPrefix() string {
	return fmt.Sprintf("%s-%s", t.Arch, t.OS)
}

// Validate checks if the Toolchain configuration is valid.
//
// Description:
// Verifies that both architecture and operating system are set.
//
// Parameters:
//   - (none)
//
// Returns:
//   - error: Nil if valid, otherwise an error describing the validation failure
//
// Edge cases:
//   - Empty Arch: returns error "architecture not specified"
//   - Empty OS: returns error "operating system not specified"
//   - Both empty: returns first error (architecture checked first)
func (t *Toolchain) Validate() error {
	if t.Arch == "" {
		return fmt.Errorf("architecture not specified")
	}
	if t.OS == "" {
		return fmt.Errorf("operating system not specified")
	}
	return nil
}

// String returns a human-readable string representation.
//
// Description:
// Shows the architecture, operating system, and the C/C++ compiler paths.
//
// Example: "arm64-android (aarch64-linux-android-gcc/aarch64-linux-android-g++)"
//
// Parameters:
//   - (none)
//
// Returns:
//   - string: String representation of the toolchain
func (t *Toolchain) String() string {
	return fmt.Sprintf("%s-%s (%s/%s)", t.Arch, t.OS, t.CC, t.CXX)
}

// ParseArchitecture parses a string representation of an architecture.
//
// Description:
// Converts string to Architecture type. Accepts various common aliases and is case-insensitive.
//
// How it works:
// Supported input strings:
//   - "arm", "ARM" -> Arm
//   - "arm64", "aarch64", "ARM64", "AARCH64" -> Arm64
//   - "x86", "i386", "i686", "X86" -> X86
//   - "x86_64", "amd64", "X86_64", "AMD64" -> X86_64
//
// Parameters:
//   - s: String representation of architecture (case-insensitive)
//
// Returns:
//   - Architecture: Parsed architecture constant
//   - error: Error if the string cannot be parsed
//
// Edge cases:
//   - Case-insensitive matching: "ARM", "Arm", "arm" all work
//   - Alias support: "aarch64" maps to Arm64, "amd64" maps to X86_64
//   - Unknown string: returns descriptive error
func ParseArchitecture(s string) (Architecture, error) {
	s = strings.ToLower(s)
	switch s {
	case "arm":
		return Arm, nil
	case "arm64", "aarch64":
		return Arm64, nil
	case "x86", "i386", "i686":
		return X86, nil
	case "x86_64", "amd64":
		return X86_64, nil
	default:
		return "", fmt.Errorf("unknown architecture: %s", s)
	}
}

// ParseOS parses a string representation of an operating system.
//
// Description:
// Converts string to OS type. Accepts various common aliases and is case-insensitive.
//
// How it works:
// Supported input strings:
//   - "linux", "LINUX" -> Linux
//   - "windows", "WINDOWS" -> Windows
//   - "darwin", "macos", "DARWIN", "MACOS" -> Darwin
//   - "android", "ANDROID" -> Android
//
// Parameters:
//   - s: String representation of OS (case-insensitive)
//
// Returns:
//   - OS: Parsed OS constant
//   - error: Error if the string cannot be parsed
//
// Edge cases:
//   - Case- insensitive matching: "LINUX", "Linux", "linux" all work
//   - Alias support: "macos" maps to Darwin
//   - Unknown string: returns descriptive error
func ParseOS(s string) (OS, error) {
	s = strings.ToLower(s)
	switch s {
	case "linux":
		return Linux, nil
	case "windows":
		return Windows, nil
	case "darwin", "macos":
		return Darwin, nil
	case "android":
		return Android, nil
	default:
		return "", fmt.Errorf("unknown operating system: %s", s)
	}
}
