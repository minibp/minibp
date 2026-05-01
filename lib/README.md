## Project Structure

```
minibp/
├── cmd/minibp/         # CLI entry point
├── lib/
│   ├── parser/         # Blueprint lexer, parser, AST, evaluator
│   │   ├── ast.go      # AST definitions (Module, Select, Unset, etc.)
│   │   ├── ast_json.go # JSON serialization with sync.Pool optimization
│   │   ├── lexer.go    # Tokenizer (IDENT, STRING, UNSET, AT, etc.)
│   │   ├── parser.go   # Recursive descent parser
│   │   └── eval.go     # Evaluator (select, operators, variables, strict mode)
│   ├── module/         # Module type registry & factories
│   │   ├── module.go   # Module interface (Name, Type, Srcs, Deps, Props)
│   │   ├── registry.go # Thread-safe module registry with Factory pattern
│   │   └── types.go    # Built-in module types (CC, Go, Java, Proto, etc.)
│   ├── dag/            # DAG dependency graph & topological sort
│   │   └── graph.go    # Kahn's algorithm O(V) topological sort
│   ├── dependency/    # Dependency resolution
│   │   └── graph.go    # Dependency graph with reverse edges
│   ├── ninja/          # Ninja generator & rules
│   │   ├── gen.go      # Build file generation
│   │   ├── rules.go    # Build rule interfaces, ApplyDefaults, module references
│   │   ├── writer.go   # Ninja output writer with 64KB buffer
│   │   ├── cc.go       # C/C++ rules with strings.Builder optimization
│   │   ├── go.go       # Go rules (go_library, go_binary, go_test)
│   │   ├── java.go     # Java rules (java_library, java_binary, etc.)
│   │   ├── filegroup.go # File group rules with ** glob support
│   │   ├── custom.go   # Custom and proto rules
│   │   ├── defaults.go # Defaults, package, soong_namespace, phony, sh/python rules
│   │   ├── prebuilt.go # Prebuilt module rules
│   │   ├── genrule.go  # Genrule support
│   │   └── helpers.go  # Property extraction helpers (getGoTargetVariants, etc.)
│   ├── toolchain/      # Compiler/toolchain configuration
│   ├── errors/         # Enhanced error handling with BuildError, categories, suggestions
│   ├── hasher/         # File hashing with SHA256
│   ├── incremental/    # Incremental build with AST caching
│   │   ├── manager.go  # Hash-based file change detection, JSON cache
│   │   └── merge.go    # Cache merging utilities
│   ├── glob/           # Glob pattern expansion (thread-safe with RWMutex)
│   │   └── glob.go     # Iterative ** matching (no recursion depth limits)
│   ├── props/          # Property extraction helpers
│   │   └── props.go    # O(1) property lookup via propMap cache
│   ├── pathutil/       # Path utilities with traversal protection
│   │   └── pathutil.go # filepath.Clean-based path sanitization
│   ├── namespace/      # Soong namespace resolution
│   │   └── namespace.go # Namespace imports and //namespace:module resolution
│   ├── variant/        # Architecture variant handling
│   │   └── variant.go  # host_supported/device_supported filtering
│   ├── utils/          # Utility functions
│   └── version/        # Version info
└── examples/           # Example build files
```

## Cross-Compilation Support

The Go build rules in `lib/ninja/go.go` support cross-compilation:

- **go_binary**: Builds both host and target binaries when using `-os` / `-arch` flags
- **go_library**: Builds library archives for target platform
- **go_test**: Builds test binaries for both host and target

### Output Naming

- **Windows targets**: Add `.exe` extension automatically (e.g., `minibp_windows_amd64.exe`)
- **Host builds**: Named with runtime platform suffix (e.g., `minibp_linux_arm64`)
- **Phony rules**: Reference both host and target outputs (e.g., `build minibp: phony minibp_linux_arm64 minibp_windows_amd64.exe`)
