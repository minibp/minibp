# AGENTS.md

## Build & Run
- Build: `go build -o minibp cmd/minibp/main.go`
- Never use `go run` — it points to temp `/tmp/go-build` paths, breaking ninja rebuilds
- Parse single file: `./minibp Android.bp`
- Parse directory: `./minibp -a .`
- Output to file: `./minibp -o build.ninja Android.bp`

## Testing
Run in order:
1. `go test ./...`
2. `go build -o minibp cmd/minibp/main.go && ./minibp -a . && ninja -v`
3. `cd examples && ../minibp -a . && ninja -v`

Integration tests require: Java, GCC, G++, Ninja-build installed.

## Code Style
- Run `go fmt` before commits
- Exported: `MixedCaps`, unexported: `camelCase`
- No meaningless identifiers
- Comments must be English

## Commit Messages
- Capitalized, ≤50 chars title
- Body ≤200 chars (what/why/how)
- No [Conventional Commits](https://www.conventionalcommits.org/)
- Use `git am`/`git cherry-pick` to preserve patch info

## Architecture
- `cmd/minibp/` — CLI entry point
- `lib/parser/` — Blueprint lexer, parser, AST, evaluator
- `lib/ninja/` — Ninja generator (cc.go, golang.go, java.go, etc.)
  - `cc.go` — C/C++ rules with undefines, version management, soname symlinks
  - `config.go` — Config file generation from templates (config_gen)
  - `replace.go` — File replace rules for source file preprocessing
- `lib/module/` — Module registry & types
- `lib/dag/` — Dependency graph, topological sort
- `lib/incremental/` — Hash-based caching in `.minibp/`

## Quirks
- Incremental builds use SHA256 file hashes cached in `.minibp/`
- `exec_script()` is minibp-specific (not standard Soong) — runs at parse time
- Select statements support `any @ var` binding and `unset` keyword
- Defaults modules use additive list merging (not replacing)
