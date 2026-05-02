# minibp

A minimal Android.bp (Blueprint) parser and Ninja build file generator written in Go.

## Features

- **Full Soong-style build rules**: Supports 25+ module types including:
  - C/C++: `cc_library`, `cc_library_static`, `cc_library_shared`, `cc_object`, `cc_binary`, `cc_library_headers`, `cc_test`, `cpp_library`, `cpp_binary`
  - Go: `go_library`, `go_binary`, `go_test`
  - Java: `java_library`, `java_library_static`, `java_library_host`, `java_binary`, `java_binary_host`, `java_test`, `java_import`
  - Proto: `proto_library`, `proto_gen`
  - Shell/Python: `sh_binary_host`, `python_binary_host`, `python_test_host`
  - Other: `filegroup`, `custom`, `phony`

- **Soong syntax support**:
  - `defaults` modules for property reuse (lists are additively merged)
  - `package` modules for package-level defaults
  - `soong_namespace` with `//namespace:module` reference resolution and `imports`
  - Module references: `:module` and `:module{.tag}` syntax
  - Visibility control: `//visibility:public`, `//visibility:private`, etc.
  - `host_supported` / `device_supported` for build target filtering

- **select() statements** for conditional compilation:
  - Single variable: `select(arch, { arm: [...], default: [...] })`
  - Multi-variable (tuple): `select((arch(), os()), { ("arm","linux"): [...], default: [...] })`
  - `soong_config_variable(namespace, var)` for external config-driven selects
  - `any @ var` binding: wildcard match that binds the matched value to a variable
  - `unset` keyword: treat a property as never assigned
  - Strict mode (default): unmatched select without `default` reports an error

- **Operator support**:
  - `+` on strings: concatenation
  - `+` on integers: arithmetic addition
  - `+` on lists: `["a"] + ["b"]` → `["a","b"]`
  - `+` on maps: recursive merge (lists appended, scalars overridden)
  - `+=` for variable concatenation

- **Desc comments**: Generate Soong-style build descriptions

- **Transitive header includes**: Option B style - if A depends on B, and B depends on C, A automatically includes C's headers

- **Wildcard support**: `filegroup` supports `**` recursive glob patterns

- **Custom commands**: Full support for `$in` and `$out` variables in custom rules

- **Duplicate rule handling**: Avoids duplicate ninja rule definitions

- **exec_script() extension** (minibp-specific, not standard Soong): Run external scripts during Blueprint parsing/evaluation phase. Captures stdout as expression value, with automatic JSON parsing for structured output.

- **config_gen support**: Generate header files from templates with automatic include path injection. CC modules depending on config_gen automatically get `-I configdir` and implicit header dependencies.

- **Incremental builds**: SHA256-based file hash caching with AST JSON serialization in `.minibp/` directory

## Usage

```bash
# Build the binary (recommended over go run)
go build -o minibp cmd/minibp/main.go

# Parse a single .bp file
./minibp Android.bp

# Parse all .bp files in a directory
./minibp -a .

# Specify output file
./minibp -o build.ninja Android.bp

# Specify custom toolchain (linker)
./minibp -cc clang -cxx clang++ -ld lld -a .
```

> **Note**: Avoid `go run` for generating ninja files — it causes regeneration on each execution, breaking subsequent ninja builds.

## Example

```bash
cd examples
../minibp -a .
ninja
```

## Blueprint Syntax Examples

```bp
# Variable assignment with operators
common_flags = ["-Wall", "-Werror"]
debug_flags = common_flags + ["-g", "-DDEBUG"]

# Single-variable select
cc_library {
    name: "libfoo",
    srcs: select(arch, {
        arm: ["foo_arm.S"],
        arm64: ["foo_arm64.S"],
        default: ["foo_generic.c"],
    }),
    cflags: debug_flags,
}

# Multi-variable select
cc_binary {
    name: "app",
    srcs: select((arch(), os()), {
        ("arm", "linux"): ["arm_linux.c"],
        ("x86_64", "linux"): ["x86_linux.c"],
        default: ["generic.c"],
    }),
}

# select with soong_config_variable
cc_library {
    name: "libbar",
    cflags: select(soong_config_variable("acme", "board"), {
        "soc_a": ["-DSOC_A"],
        default: [],
    }),
}

# select with any @ var binding
cc_binary {
    name: "cross_app",
    cflags: select(os, {
        "linux": ["-DLINUX"],
        any @ my_os: ["-D" + my_os],
    }),
}

# select with unset
cc_library {
    name: "libbaz",
    enabled: select(os, {
        "darwin": false,
        default: unset,
    }),
}

# Defaults with additive list merging
defaults {
    name: "my_defaults",
    cflags: ["-Wall"],
}

cc_binary {
    name: "app",
    defaults: ["my_defaults"],
    cflags: ["-O2"],  # Final: ["-O2", "-Wall"] — defaults appended
}

# Host/device filtering
cc_binary {
    name: "host_tool",
    host_supported: true,
    srcs: ["tool.c"],
}

# Phony targets
phony {
    name: "all",
    deps: [":app", ":libfoo"],
}

# Namespace resolution
soong_namespace {
    name: "vendor",
    imports: ["core"],
}

# //vendor:lib resolves via namespace

# config_gen — generate header files from templates
config_gen {
    name: "my_config",
    srcs: ["config.h.in"],
    exports_include_dirs: ["out/include"],
    vars: {
        VERSION: "\"1.0.0\"",
        HAS_PTHREAD: "1",
    },
}

cc_binary {
    name: "app",
    srcs: ["main.c"],
    deps: [":my_config"],  # Automatically gets -Iout/include and implicit header deps
}

> **Note**: `config_gen` is a minibp-specific extension, not part of standard Soong/Blueprint. Generated headers use `${VAR}` and `${define VAR}` syntax.

# exec_script() — minibp extension, runs at parse time
value = exec_script("detect_arch.sh")
cflags: ["-DARCH=" + exec_script("get_flag.sh", "arg1")]
```

> **Note**: `exec_script()` is a minibp-specific extension, not part of standard Soong/Blueprint syntax. The script runs during parsing, and its stdout (trimmed, with optional JSON parsing) becomes the expression value.

## Building

```bash
go build -o minibp cmd/minibp/main.go
```

## Testing

```bash
# Unit tests
go test ./...

# Integration test
go build -o minibp cmd/minibp/main.go && ./minibp -a . && ninja
cd examples && ../minibp -a . && ninja
```
