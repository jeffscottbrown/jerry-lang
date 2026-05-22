# Removing Go from the Jerry Compiler

The self-hosted compiler (`self-host/*.jer`) already generates correct LLVM IR
for real programs. This checklist tracks the work to remove Go from the pipeline
entirely so the `jerry` binary is itself compiled from Jerry.

**Current state (2026-05-21):** 239 Jerry tests passing. Self-hosted compiler
is fully bootstrapped — `jerry-compiler` compiles itself (`jerry-compiler-v2`)
and all 239 tests pass through the second-generation binary. Phases 1–4 and
5a complete. Remaining: 5b (release ships pre-built jerry binary built by
jerry), Phases 6–8 (LSP in Jerry, then remove Go).

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

- [x] **3a. Decide stdlib embedding strategy**
  Install stdlib `.jer` files to `<prefix>/share/jerry/stdlib/` via
  `make install-stdlib`. The driver discovers them via `JERRY_STDLIB` env var
  or `<binary>/../share/jerry/stdlib` (Homebrew layout).

- [x] **3b. Implement `include @name` in the self-hosted compiler**
  Added `stdlib_dir_path()` builtin (all five layers). `main.jer` scans for
  `include @name` lines, strips them, reads `core.jer` + named stdlib files,
  and prepends their content before parsing. Fixed `gen_and`/`gen_or` phi
  predecessor bug exposed by complex boolean conditions in the new code.

- [x] **3c. Update Homebrew formula to install stdlib files**
  ```ruby
  (share/"jerry"/"stdlib").install Dir["stdlib/*.jer"]
  ```

---

## Phase 4 — Replace Go Compilation Pipeline

- [x] **4a. Make `jerry compile/run/ir` invoke the self-hosted binary**
  `cmd/jerry/main.go` is a thin shim: `findJerryCompiler()` checks
  `JERRY_COMPILER` env, then `<binary-dir>/jerry-compiler`, then PATH.
  `compile`/`run`/`ir`/`test` all delegate compilation to jerry-compiler.
  `cmdTest` replaced Go parser with text-scanning (`bufio.Scanner`) to find
  `fn test_*()` functions. `cmdSweep` likewise uses text scanning.
  Hidden `jerry _ir` subcommand uses the Go codegen pipeline directly —
  bootstrap escape hatch for building jerry-compiler itself.

- [x] **4b. Build `jerry-compiler` binary as part of the release process**
  CI: `jerry _ir self-host/*.jer | clang ... -o jerry-compiler`; both binaries
  bundled in the release archive. Homebrew formula builds jerry-compiler in
  `def install` using `jerry _ir` + `ENV.cc`. `make build-compiler` target
  added for local bootstrap.

- [ ] **4c. Remove `internal/codegen`** — blocked until Phase 7 (LSP in Jerry) is complete
- [ ] **4d. Remove `internal/checker`** — blocked until Phase 7
- [ ] **4e. Remove `internal/parser`** — blocked until Phase 7
- [ ] **4f. Remove `internal/ast`** — blocked until Phase 7
- [ ] **4g. Remove `internal/build`** — blocked until Phase 7 and until `jerry _ir` bootstrap
  is no longer needed (Phase 8: Homebrew downloads pre-built jerry-compiler instead of
  building from Go source)

---

## Phase 5 — Bootstrap

- [x] **5a. Compile `self-host/*.jer` using the self-hosted compiler itself**
  True self-hosting: the self-hosted compiler compiles itself. Compare output
  IR against the Go-compiled version to verify correctness.
  Fixed self-hosted codegen bug: `string_contains`/`string_starts_with`/
  `string_ends_with` were missing IR declarations and `gen_fn_call` cases,
  causing them to fall through to the user-function path and emit wrong name
  and void return type. Added tests in `tests/strings_test.jer`.
  CI now bootstraps a `jerry-compiler-v2` and runs smoke tests + Jerry tests
  through it (239 passing).

- [ ] **5b. The `jerry` binary is now a Jerry binary**
  The release ships a pre-compiled `jerry` binary built by an earlier version
  of jerry, plus source — the same bootstrap pattern Go itself uses.

---

## Phase 6 — Jerry Language Capabilities for LSP

The LSP server uses JSON-RPC 2.0 over stdio and requires Jerry to be able to
parse and emit JSON, read a fixed number of bytes from stdin (Content-Length
framing), and convert strings to integers.  These additions also benefit
general-purpose Jerry programs.

- [ ] **6a. Add `read_bytes(n: int): string` builtin**
  Read exactly `n` bytes from stdin and return them as a string.  Wire through
  all five layers.  This is the primitive needed to read JSON-RPC message bodies
  after parsing the `Content-Length` header.

- [ ] **6b. Add `string_index_of(s: string, sub: string): int` builtin**
  Return the 0-based index of the first occurrence of `sub` in `s`, or `-1` if
  not found.  Wire through all five layers.  Needed for JSON parsing (finding
  `:`, `"`, `{`, `}`, etc.) and for splitting the `Content-Length` header line.

- [ ] **6c. Add `string_to_int(s: string): int` builtin**
  Parse a decimal integer string and return its value.  Wire through all five
  layers.  Needed to convert the `Content-Length` header value to an integer.

- [ ] **6d. Implement `stdlib/json.jer` — JSON parser and serializer in Jerry**
  Define a `JsonValue` class with a `kind` discriminant
  (`NULL=0 BOOL=1 INT=2 FLOAT=3 STRING=4 ARRAY=5 OBJECT=6`) and typed fields
  (`bool_val`, `int_val`, `float_val`, `str_val`, `arr_val: JsonValue[]`,
  `obj_keys: string[]`, `obj_vals: JsonValue[]`).
  Implement `json_parse(s: string): JsonValue` (recursive descent using
  `string_index_of`, `char_at`, `string_slice`) and
  `json_stringify(v: JsonValue): string`.
  Expose helpers: `json_get_string`, `json_get_int`, `json_get_bool`,
  `json_get_object`, `json_build_object`, `json_set_string`, etc.
  Install to `stdlib/json.jer`; usable via `include @json`.

---

## Phase 7 — LSP Server in Jerry

Port `internal/lsp` (451 lines across 5 Go files) to a standalone
`jerry-lsp` binary written in Jerry.  The binary speaks LSP over stdio,
delegating parse+check to the self-hosted compiler pipeline.

- [ ] **7a. JSON-RPC stdio transport (`lsp/transport.jer`)**
  Read `Content-Length: N\r\n\r\n` header then `N` bytes via `read_bytes`.
  Dispatch to handler by `method` field.  Write responses with correct
  `Content-Length` framing using `write` + `flush` (add `flush_stdout()`
  builtin if needed).

- [ ] **7b. Document store (`lsp/documents.jer`)**
  `map<string, string>` keyed by URI.  `store_doc`, `load_doc`, `delete_doc`.

- [ ] **7c. Diagnostics (`lsp/diagnostics.jer`)**
  On `textDocument/didOpen` and `textDocument/didChange`, run the self-hosted
  parse → type_check pipeline on the document text (reusing the same code as
  `jerry-compiler`).  Convert `ParseError` / `CheckError` positions to LSP
  0-based `Range` and emit `textDocument/publishDiagnostics`.
  Strip `include` lines before parsing (same as `main.jer`) and prepend
  `core.jer` + named stdlib files.

- [ ] **7d. Completions (`lsp/completion.jer`)**
  Build a static list of Jerry keywords, primitive types, bool literals, and
  known builtins/stdlib functions (matching the current Go list).  On
  `textDocument/completion`, parse the document text to extract user-defined
  `fn` and `class` names and append them to the static list.  Return as a
  JSON array of `CompletionItem` objects.

- [ ] **7e. Code lens + execute command (`lsp/codelens.jer`)**
  On `textDocument/codeLens`, parse the document and find `fn main()` with no
  params.  Return a `▶ Run` lens at that position using command `jerry.run`.
  On `workspace/executeCommand` for `jerry.run`, invoke
  `exec(["jerry", "run", file_path])` and notify the client with the output via
  `window/showMessage`.

- [ ] **7f. LSP server main + event loop (`lsp/main.jer`)**
  Handle `initialize` (advertise TextDocumentSync=Full, CompletionProvider,
  CodeLensProvider, ExecuteCommandProvider), `initialized`, `shutdown`.
  Loop: read next message → dispatch to handler → send response/notification.

- [ ] **7g. Build `jerry-lsp` binary in CI, release, and Homebrew**
  CI: `jerry _ir lsp/*.jer > jerry-lsp-bootstrap.ll && cc ... -o jerry-lsp`.
  Release archive includes `jerry-lsp`.  Homebrew `def install` builds it the
  same way as `jerry-compiler` and installs to `bin/jerry-lsp`.

- [ ] **7h. Update `jerry lsp` in `cmd/jerry/main.go`**
  `findJerryLsp()` checks `JERRY_LSP` env → `<exe-dir>/jerry-lsp` → PATH.
  `cmdLsp` execs it with inherited stdio (same pattern as `jerry-compiler`).

- [ ] **7i. Remove `internal/lsp` Go package**
  Delete the five Go files.  Remove the `glsp` dependency from `go.mod`.

- [ ] **7j. Remove blocked Go internal packages (4c–4f)**
  `internal/lsp` was the last consumer.  Delete `internal/codegen`,
  `internal/checker`, `internal/parser`, `internal/ast`.
  `internal/build` stays until Phase 8 eliminates the `jerry _ir` bootstrap.

---

## Phase 8 — Remove Go Entirely

- [ ] **8a. Drop `depends_on "go" => :build` from Homebrew formula**
  The formula downloads pre-compiled `jerry`, `jerry-compiler`, and `jerry-lsp`
  from the GitHub release (or bootstraps from a known-good previous release)
  instead of building from Go source.  `jerry _ir` and `internal/build` are no
  longer needed; remove them.

- [ ] **8b. Delete `internal/build` and the hidden `jerry _ir` subcommand**
  The last remaining Go internal package.  The Homebrew formula no longer builds
  from source.  CI bootstrap switches to downloading a pinned release binary.

- [ ] **8c. Delete all remaining Go source files**
  `cmd/jerry/main.go` becomes a Jerry program; `go.mod` / `go.sum` deleted.

- [ ] **8d. Update CI/CD**
  Remove `actions/setup-go`, `go vet`, `go test`, and Go build steps.
  Add jerry-native test runner (`jerry-compiler` + `jerry test`).
  Pin a bootstrap release binary for the CI self-hosting step.
