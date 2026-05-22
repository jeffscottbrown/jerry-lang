# Jerry

[![CI](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/ci.yml/badge.svg)](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/ci.yml)
[![Release](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/release.yml/badge.svg)](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/jeffscottbrown/jerry-lang)](https://github.com/jeffscottbrown/jerry-lang/releases/latest)

Jerry is a statically-typed, JavaScript-style language that compiles to native binaries via LLVM IR. The compiler is self-hosted — written in Jerry itself.
The language is experimental and a proof of concept.

📖 **[Read the Jerry Language Guide](docs/language.md)** for a full tour of the language, standard library, and remote modules.

```jerry
fn main() {
    let name: string = "world";
    print("Hello, " + name + "!");

    let nums: int[] = [1, 2, 3, 4, 5];
    let sum: int = 0;
    for (let i: int = 0; i < len(nums); i++) {
        sum = sum + nums[i];
    }
    print("sum = " + int_to_string(sum));
}
```

## Built with Jerry

✨ **[gdgrep](https://github.com/jeffscottbrown/gdgrep)** — a fast, friendly
`grep` replacement written entirely in Jerry. It supports case-insensitive
matching (`-i`), line numbers (`-n`), multi-file labels, and stdin pipelines,
and ships pre-built binaries for macOS (arm64, x86_64) and Linux (x86_64).

Install it with one command:

```sh
# Homebrew (macOS and Linux)
brew tap jeffscottbrown/gdgrep
brew install gdgrep

# or download a pre-built binary from the releases page
curl -fsSL https://github.com/jeffscottbrown/gdgrep/releases/latest/download/gdgrep-macos-arm64.tar.gz | tar -xz
```

Then use it like any other grep:

```sh
gdgrep -n TODO src/main.jer
cat access.log | gdgrep 404
```

`gdgrep` is a great real-world reference for how to structure a multi-file
Jerry project, parse command-line flags, read files and stdin, and ship a
released binary with a Homebrew tap. Browse its source at
[`github.com/jeffscottbrown/gdgrep`](https://github.com/jeffscottbrown/gdgrep).

## Requirements

Jerry requires **clang** to link compiled programs.

- **macOS** — clang is included with Xcode Command Line Tools: `xcode-select --install`
- **Linux** — install via your package manager: `apt install clang` / `dnf install clang`

## Installation

### curl (macOS and Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/jeffscottbrown/jerry-lang/main/install.sh | bash
```

Pin a specific version or choose a custom install directory:

```sh
JERRY_VERSION=v0.0.3 JERRY_INSTALL_DIR=~/.local/bin bash <(curl -fsSL https://raw.githubusercontent.com/jeffscottbrown/jerry-lang/main/install.sh)
```

### Homebrew (macOS and Linux)

```sh
brew tap jeffscottbrown/jerry
brew install jerry
```

### Download from GitHub Releases

Pre-built binaries for macOS (x86_64, arm64) and Linux (x86_64) are available on the [Releases page](https://github.com/jeffscottbrown/jerry-lang/releases/latest).

1. Download the archive for your platform.
2. Verify the checksum against `checksums.txt`.
3. Extract and move the `jerry` and `jerry-compiler` binaries onto your `PATH`.

```sh
# Example for macOS Apple Silicon
curl -fsSL https://github.com/jeffscottbrown/jerry-lang/releases/latest/download/jerry-macos-arm64.tar.gz | tar -xz
sudo mv jerry jerry-compiler /usr/local/bin/
```

### Build from source

Requires Go 1.26+ and clang (Go is used for the bootstrap shim; `jerry-compiler` is built from Jerry source).

```sh
git clone https://github.com/jeffscottbrown/jerry-lang.git
cd jerry-lang
make build-compiler   # builds jerry + jerry-compiler
sudo cp bin/jerry bin/jerry-compiler /usr/local/bin/
```

## Usage

```sh
jerry run   hello.jer               # compile and run immediately
jerry compile hello.jer -o hello    # compile to a native binary
jerry ir    hello.jer               # dump LLVM IR to stdout
jerry --version                     # print version
```

### Remote modules

Jerry supports remote modules hosted on GitHub.

```sh
jerry get github.com/jeffscottbrown/jerry-string@v0.0.1   # fetch and pin a module
jerry sweep                                                 # sync jerry.remotes / jerry.sum
```

In your source file:

```jerry
include "github.com/jeffscottbrown/jerry-string"

fn main() {
    let words: string[] = split_whitespace("the quick brown fox");
    print(words[1]);   // quick
}
```

Module versions are tracked in `jerry.remotes` and hashes in `jerry.sum`, both located alongside your source files.

## GitHub Actions

The `setup-jerry` composite action installs the Jerry compiler on any GitHub-hosted runner. No marketplace listing required — reference it directly from this repo.

```yaml
- uses: jeffscottbrown/jerry-lang/.github/actions/setup-jerry@main
```

Pin a specific version for reproducible builds:

```yaml
- uses: jeffscottbrown/jerry-lang/.github/actions/setup-jerry@main
  with:
    version: v0.1.4
```

### Full project workflow

A complete workflow that builds on all platforms and creates a GitHub Release on `v*.*.*` tag pushes is available at [`examples/workflows/release.yml`](examples/workflows/release.yml). Copy it to your project at `.github/workflows/release.yml` and replace `myapp` with your binary name.

Key steps:

```yaml
- name: Install clang (Linux only)
  if: runner.os == 'Linux'
  run: sudo apt-get install -y clang

- name: Setup Jerry
  uses: jeffscottbrown/jerry-lang/.github/actions/setup-jerry@main

- name: Build
  run: jerry compile main.jer -o myapp
```

The action automatically detects the runner OS and architecture (Linux x86_64, macOS x86_64, macOS arm64), downloads the matching pre-built binary, verifies its checksum, and adds `jerry` to `PATH`.

## Language features

| Feature | Example |
|---|---|
| Static types | `let x: int = 42;` |
| Functions | `fn add(a: int, b: int): int { return a + b; }` |
| Classes | `class Point { x: int; y: int; }` |
| Arrays | `let nums: int[] = [1, 2, 3];` |
| Maps | `let m: map<string, int> = {"a": 1}; m["b"] = 2;` |
| Closures | `let double = fn(x: int): int { return x * 2; };` |
| For / while | `for (let i: int = 0; i < 10; i++) { ... }` |

For a complete, runnable walkthrough of types, control flow, classes, the
standard library, and the [`jerry-string`](https://github.com/jeffscottbrown/jerry-string)
and [`jerry-logging`](https://github.com/jeffscottbrown/jerry-logging) remote
modules, see the **[Jerry Language Guide](docs/language.md)**.

## Documentation

- **[Language Guide](docs/language.md)** — practical tour of every feature with runnable snippets.
- [`stdlib/core.jer`](stdlib/core.jer) — always-available numeric and bool helpers.
- [`stdlib/time.jer`](stdlib/time.jer) — `Timer` class and `millis_to_duration`.
- [`stdlib/json.jer`](stdlib/json.jer) — JSON parser, serializer, and object helpers.
- [`examples/`](examples/) — short, self-contained programs.
- [**gdgrep**](https://github.com/jeffscottbrown/gdgrep) — full-sized utility written in Jerry; a good reference project.

## Examples

The [`examples/`](examples/) directory contains runnable programs:

```sh
jerry run examples/hello.jer
jerry run examples/fibonacci.jer
jerry run examples/classes.jer
jerry run examples/closures.jer
jerry run examples/strings.jer
jerry run examples/logging.jer        # uses the jerry-logging remote module
jerry run examples/wordcount.jer      # combines jerry-string + jerry-logging
```

## License

Licensed under the [Apache License, Version 2.0](LICENSE). See the [`LICENSE`](LICENSE) file for the full text.
