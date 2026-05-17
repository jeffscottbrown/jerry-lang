# Jerry

[![CI](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/ci.yml/badge.svg)](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/ci.yml)
[![Release](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/release.yml/badge.svg)](https://github.com/jeffscottbrown/jerry-lang/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/jeffscottbrown/jerry-lang)](https://github.com/jeffscottbrown/jerry-lang/releases/latest)

Jerry is a statically-typed, JavaScript-style language that compiles to native binaries via LLVM IR. The compiler is written in Go.

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

### go install

Requires Go 1.21+.

```sh
go install github.com/jeffscottbrown/jerry-lang/cmd/jerry@latest
```

### Download from GitHub Releases

Pre-built binaries for macOS (x86_64, arm64) and Linux (x86_64) are available on the [Releases page](https://github.com/jeffscottbrown/jerry-lang/releases/latest).

1. Download the archive for your platform.
2. Verify the checksum against `checksums.txt`.
3. Extract and move the `jerry` binary onto your `PATH`.

```sh
# Example for macOS Apple Silicon
curl -fsSL https://github.com/jeffscottbrown/jerry-lang/releases/latest/download/jerry-macos-arm64.tar.gz | tar -xz
sudo mv jerry-macos-arm64 /usr/local/bin/jerry
```

### Build from source

Requires Go 1.21+ and clang.

```sh
git clone https://github.com/jeffscottbrown/jerry-lang.git
cd jerry-lang
go build -o jerry ./cmd/jerry
sudo mv jerry /usr/local/bin/
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

## Language features

| Feature | Example |
|---|---|
| Static types | `let x: int = 42;` |
| Functions | `fn add(a: int, b: int): int { return a + b; }` |
| Classes | `class Point { x: int; y: int; }` |
| Arrays | `let nums: int[] = [1, 2, 3];` |
| Closures | `let double = fn(x: int): int { return x * 2; };` |
| For / while | `for (let i: int = 0; i < 10; i++) { ... }` |

## Examples

The [`examples/`](examples/) directory contains runnable programs:

```sh
jerry run examples/hello.jer
jerry run examples/fibonacci.jer
jerry run examples/classes.jer
jerry run examples/closures.jer
jerry run examples/strings.jer
```

## License

MIT
