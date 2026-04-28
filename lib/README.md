## Project Structure

```
minibp/
├── cmd/minibp/         # CLI entry point
├── lib/
│   ├── parser/         # Blueprint lexer, parser, AST, evaluator
│   │   ├── ast.go      # AST definitions (Module, Select, Unset, etc.)
│   │   ├── lexer.go    # Tokenizer (IDENT, STRING, UNSET, AT, etc.)
│   │   ├── parser.go   # Recursive descent parser
│   │   └── eval.go     # Evaluator (select, operators, variables, strict mode)
│   ├── module/         # Module type registry & factories
│   ├── dag/            # DAG dependency graph & topological sort
│   ├── ninja/          # Ninja generator & rules
│   │   ├── gen.go      # Build file generation
│   │   ├── rules.go    # Build rule interfaces, ApplyDefaults, module references
│   │   ├── writer.go   # Ninja output writer
│   │   ├── cc.go       # C/C++ rules (cc_library, cc_binary, cc_test, etc.)
│   │   ├── go.go       # Go rules (go_library, go_binary, go_test)
│   │   ├── java.go     # Java rules (java_library, java_binary, etc.)
│   │   ├── filegroup.go # File group rules
│   │   ├── custom.go   # Custom and proto rules
│   │   ├── defaults.go # Defaults, package, soong_namespace, phony, sh/python rules
│   │   └── helpers.go  # Property extraction helpers (getGoTargetVariants, etc.)
│   ├── toolchain/      # Compiler/toolchain configuration
│   ├── errors/         # Error handling
│   ├── hasher/         # File hashing
│   ├── dependency/    # Dependency resolution
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