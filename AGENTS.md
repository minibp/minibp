# minibp Repository Agent Guide

## Overview
minibp is a Go-based Android Blueprint (.bp) parser and Ninja build file generator implementing Soong-style build rules for Go, C/C++, Java, Proto, Shell/Python, and other languages.

## Repository Structure
```
minibp/
├── cmd/minibp/       # CLI entry point
├── lib/
│   ├── parser/       # Lexer, parser, AST, evaluator (Soong syntax)
│   ├── module/       # Module type registry & factories
│   ├── build/        # Build pipeline (collect + variant merging)
│   ├── dag/          # Dependency graph & topological sort
│   ├── ninja/        # Ninja generator, rules, writers (cc.go, go.go, java.go, defaults.go)
│   ├── glob/         # Recursive glob pattern matching (**)
│   ├── hasher/       # File hashing for regeneration
│   ├── variant/      # Architecture/platform variant handling
│   ├── props/        # Property extraction helpers
│   ├── toolchain/    # Toolchain configuration
│   └── errors/       # Error handling
└── examples/         # Example build files and usage
```

## Critical Build Pipeline Order
1. ParseFlags (CLI) → 2. Parse definitions → 3. ProcessAssignments → 4. CollectModules → 5. BuildNamespace → 6. BuildGraph → 7. GenerateNinja

## Key Architecture Decisions

### Variant System (lib/variant/variant.go)
- `MergeVariantProps()` applies merges in strict order: Base → Arch → Host/Target → Multilib
- List properties are **concatenated**; scalars are **overwritten**
- `IsModuleEnabledForTarget()` defaults to `true` when BOTH `host_supported` and `device_supported` are unset
- `getBoolProp()` returns `false` for missing/non-Bool properties (treated as unset)

### Select Evaluation (lib/parser/eval.go)
- `UnsetSentinel` returned when `unset` keyword used — caller must check and treat as "never assigned"
- `strictSelect` (default: true) — unmatched select without `default` produces error collection
- Multi-variable tuples supported: `select((arch(), os()), { ("arm","linux"): [...] })`
- `any @ var` binding: wildcard that binds matched value to variable

### Ninja Generation (lib/ninja/gen.go)
- Generator uses dependency injection: `Graph` interface for topological sort, `map[string]BuildRule` for implementations
- `TOPOLEVEL` parallelism: each level can build concurrently; levels build sequentially
- Rules map is registered per-module-type (cc.go, go.go, java.go, defaults.go, etc.)

### Defaults/Meta-Modules (lib/ninja/defaults.go)
- `defaults`, `cc_defaults`, `java_defaults`, `go_defaults` — property containers only (no outputs)
- `phony` — virtual alias targets
- Property inheritance: `defaults: ["name"]` merges into depending modules
- List concatenation, scalar override behavior applies

### Toolchain (lib/ninja/types.go / toolchain.go)
- `Toolchain` struct wraps CC, CXX, AR, sysroot, LTO, ccache
- `DefaultToolchain()` provides defaults; options override non-empty values only
- `LTO` accepts: `"full"`, `"thin"`, or `""` (none)
- `Ccache` = `"no"` disables; any other non-empty string enables

## Critical Constraints & Gotchas

### DO NOTs
- ❌ **Never use `go run`** for generating ninja files — causes regeneration on every execution, breaking subsequent ninja builds
- ❌ **Never modify generated files** (build.ninja, .ninja_deps) — edit source `.bp` files only
- ❌ **Never commit secrets** to repo — no `.env`, `credentials.json`, etc.

### Build Commands
```bash
# Build the binary (recommended)
go build -o minibp cmd/minibp/main.go

# Parse single file
./minibp Android.bp

# Parse directory and generate build.ninja
./minibp -a .

# With options
./minibp -o out.ninja -arch arm64 -host -variant KEY=VAL -v
```

### CLI Flags Reference
- `-o FILE` — Output ninja file (default: build.ninja)
- `-a DIR` — Scan all .bp files in directory (requires directory arg)
- `-arch ARCH` — Target architecture: arm, arm64, x86, x86_64
- `-host` — Build for host (overrides -arch)
- `-variant KEY=VAL` — Variant selector
- `-v` — Version
- `-cc/-cxx/-ar` — Compiler paths
- `-lto` — LTO mode: full, thin, none

## Testing Patterns
- Unit tests: `go test ./...`
- Integration test: build → run → ninja in both root and `examples/`
- Test files follow `*_test.go` convention in each subpackage
- Variant tests use `variant_test.go`; ninja rules tested in `ninja_test.go` and `soong_test.go`

## Module Reference Types (ninja/*.go + module/types.go)
- `cc_library`, `cc_library_static`, `cc_library_shared`, `cc_object`, `cc_binary`, `cc_test`
- `go_library`, `go_binary`, `go_test`
- `java_library`, `java_library_static`, `java_library_host`, `java_binary`, `java_test`
- `proto_library`, `proto_gen`
- `sh_binary_host`, `python_binary_host`, `python_test_host`
- `filegroup`, `custom`, `phony`

## File Hashing (lib/hasher/hasher.go)
- Used for regeneration decision: only regenerate when input files change
- Hash computed per-input-file; ninja rule depends on hash file

## Common Patterns for Agents

### Adding New Module Type
1. Add type constant in `lib/module/types.go`
2. Implement `BuildRule` in appropriate ninja package (e.g., `ninja/cc.go`)
3. Register in `ninja/gen.go` rules map
4. Add variant handling in `lib/variant/variant.go` if needed

### Debugging Parse Errors
- Check `lib/parser/parser.go` token stream via logging
- Use `lib/errors/errors.go` centralized error handling
- Validator in `lib/build/build.go` for post-parse validation

### Path Resolution
- `lib/ninja/helpers.go` provides path utilities
- `pathPrefixForOutput()` converts absolute source paths to relative for ninja
- Source directory set via `SetSourceDir()` in Generator

## Version Injection
Version set via ldflags at build time:
```
-X 'minibp/lib/version.gitTag=0.001'
```
Check `lib/version/version.go` for version handling.

## Edge Cases Summary
- Empty map/array → skip merge (no-op)
- Nil map → safe to iterate (no panic)
- Missing property → zero value / false / unset
- Duplicate module names → last write wins (AddNode)
- Circular dependency → TopoSort returns error
- Unset select branch → UnsetSentinel propagates
- Host vs device filtering → `IsModuleEnabledForTarget()` gates entire modules