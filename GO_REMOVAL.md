# Removing Go from the Jerry Compiler

The self-hosted compiler (`self-host/*.jer`) already generates correct LLVM IR
for real programs. This checklist tracks the work to remove Go from the pipeline
entirely so the `jerry` binary is itself compiled from Jerry.

**Current state (2026-05-21):** 439 tests passing. `exec(args: string[]): int`
builtin just added. Self-hosted compiler handles: primitives, strings, arrays,
maps, classes, closures, higher-order functions, recursion. All on branch
`self-hosting`.

---

## Phase 1 — Runtime Distribution

The Go binary today uses `go:embed` to bundle `runtime.c` inside itself.
The goal is to pre-compile the runtime to a static library (`jerry_runtime.a`)
at install time so the jerry binary can find it by path instead.

- [x] **1a. Add `getenv(name: string): string` builtin**
  Wire through all five layers: `runtime.c` (POSIX `getenv(3)`), `runtime.h`,
  Go IR declaration in `codegen.go`, Go codegen expr case in `codegen_expr.go`,
  Go `builtins.go`, self-hosted `codegen.jer`, self-hosted `checker.jer`.

- [x] **1b. Change `ExtractRuntime()` in `internal/build/build.go` to path-based discovery**
  Check in order:
  1. `JERRY_RUNTIME` env var (developer override)
  2. `<binary_dir>/../lib/jerry_runtime.a` (Homebrew install layout)
  3. Existing `go:embed` fallback (keeps everything working during transition)

- [x] **1c. Add `make install-runtime` target**
  Two commands: `clang -O2 -c runtime/src/runtime.c -Iruntime/src -o jerry_runtime.o`
  then `ar rcs <dest>/jerry_runtime.a jerry_runtime.o`. Used by developers
  building from source who want to opt out of the embedded runtime.

- [x] **1d. Update Homebrew formula (`../homebrew-jerry/Formula/jerry.rb`)**
  In `def install`, after `go build`, add:
  ```ruby
  system ENV.cc, "-O2", "-c", "runtime/src/runtime.c",
         "-Iruntime/src", "-o", "jerry_runtime.o"
  system "ar", "rcs", lib/"jerry_runtime.a", "jerry_runtime.o"
  ```
  The archive lands at `#{prefix}/lib/jerry_runtime.a`. Clang is guaranteed
  available on macOS (Xcode CLT is a Homebrew prerequisite).

- [ ] **1e. Verify `brew install` works end-to-end with the updated formula**
  *(requires a tagged release to test against)*

- [x] **1f. Delete `runtime/runtime.go` and the `go:embed` directive**
  Remove the go:embed fallback branch from `ExtractRuntime()`.
  The `runtime` Go package disappears from the module.

---

## Phase 2 — Self-hosted Compilation Driver

Today `self-host/main.jer` reads source from stdin and writes LLVM IR to stdout.
A real driver reads a file, generates IR, and invokes clang itself.

- [x] **2a. Rewrite `self-host/main.jer` as a file-based driver**
  - Read source file path from `args()[1]`
  - Parse `-o <output>` flag from args, default `a.out`
  - Run the existing parse → type_check → generate pipeline
  - Write IR to a temp file: `/tmp/jerry-<now_millis()>.ll`
  - Find runtime lib: `getenv("JERRY_RUNTIME")` else `<argv[0]>/../lib/jerry_runtime.a`
  - `exec(["clang", "-O1", tmp_ir, runtime_lib, "-o", out_path])`
  - Delete the temp IR file

- [x] **2b. Add self-hosted driver invocation to CI**
  Build `jerry-compiler` from `self-host/*.jer` using the Go jerry, then use it
  to compile and run examples/hello.jer, fibonacci.jer, arrays.jer, classes.jer,
  closures.jer.

- [x] **2c. Handle multiple source files / directory mode**
  The Go compiler accepts a directory and compiles all `.jer` files sorted by
  filename. Decide and implement the same in the self-hosted driver
  (likely: concatenate files in sorted order before parsing).

---

## Phase 3 — Stdlib / Include Support in Self-hosted Compiler

`include @string` etc. are parsed but ignored today. The self-hosted checker
has the stdlib builtins hardcoded, which works for the core language but won't
scale.

- [ ] **3a. Decide stdlib embedding strategy**
  Preferred: install stdlib `.jer` files to `<prefix>/share/jerry/stdlib/` at
  install time. The driver reads them from there, same as the runtime lib path
  discovery in Phase 1.

- [ ] **3b. Implement `include @name` in the self-hosted compiler**
  When the driver encounters `include @name`, it reads
  `<stdlib_dir>/name.jer` and prepends it to the source before compiling.

- [ ] **3c. Update Homebrew formula to install stdlib files**
  ```ruby
  (share/"jerry"/"stdlib").install Dir["stdlib/*.jer"]
  ```

---

## Phase 4 — Replace Go Compilation Pipeline

- [ ] **4a. Make `jerry compile/run/ir` invoke the self-hosted binary**
  `cmd/jerry/main.go` becomes a thin shim: locate the `jerry-compiler` binary
  installed alongside `jerry`, exec it with the right arguments. All actual
  compilation happens in the self-hosted binary.

- [ ] **4b. Build `jerry-compiler` binary as part of the release process**
  Release pipeline: (1) build Go `jerry`, (2) use Go `jerry` to compile
  `self-host/*.jer` into `jerry-compiler`, (3) ship both. The Homebrew formula
  runs this in `def install`.

- [ ] **4c. Remove `internal/codegen`** — Go LLVM IR generator

- [ ] **4d. Remove `internal/checker`** — Go type checker

- [ ] **4e. Remove `internal/parser`** — Go parser / Participle grammar

- [ ] **4f. Remove `internal/ast`** — Go AST definitions

- [ ] **4g. Remove `internal/build`** — Go compilation pipeline / driver

---

## Phase 5 — Bootstrap

- [ ] **5a. Compile `self-host/*.jer` using the self-hosted compiler itself**
  True self-hosting: the self-hosted compiler compiles itself. Compare output
  IR against the Go-compiled version to verify correctness.

- [ ] **5b. The `jerry` binary is now a Jerry binary**
  The release ships a pre-compiled `jerry` binary built by an earlier version
  of jerry, plus source — the same bootstrap pattern Go itself uses.

---

## Phase 6 — Remove Go Entirely

- [ ] **6a. Drop `depends_on "go" => :build` from Homebrew formula**
  The formula downloads a pre-compiled jerry binary (or bootstraps from a
  known-good previous release) instead of building from Go source.

- [ ] **6b. Delete all remaining Go source files**

- [ ] **6c. Update CI/CD**
  Remove `go test`, remove Go toolchain setup steps, add jerry-native test runner.
