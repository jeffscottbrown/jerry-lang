.PHONY: build build-compiler build-test-runner build-create build-sweep build-main build-lsp build-get test install install-runtime install-stdlib run-hello run-fibonacci run-arrays run-classes run-closures run-strings clean

# Inject version from the most recent git tag if available, else "dev".
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
LDFLAGS = -X main.Version=$(VERSION)

# For local development, fall back to go run ./cmd/jerry if bin/jerry-native doesn't exist.
JERRY = go run ./cmd/jerry

build:
	go build -ldflags "$(LDFLAGS)" -o bin/jerry ./cmd/jerry

# Build the jerry-lsp and jerry-get Go binaries (still Go until LSP/HTTP are ported).
build-lsp:
	go build -ldflags "$(LDFLAGS)" -o bin/jerry-lsp ./cmd/jerry-lsp

build-get:
	go build -ldflags "$(LDFLAGS)" -o bin/jerry-get ./cmd/jerry-get

# Rebuild the self-hosted jerry-compiler binary from source using a seed binary.
# The seed is the jerry-compiler currently on PATH (e.g. installed via Homebrew),
# or override with: make build-compiler JERRY_COMPILER_SEED=/path/to/jerry-compiler
JERRY_COMPILER_SEED ?= jerry-compiler

build-compiler: install-runtime install-stdlib
	JERRY_RUNTIME=$(PREFIX)/lib/jerry_runtime.a \
	JERRY_STDLIB=$(PREFIX)/share/jerry/stdlib \
		$(JERRY_COMPILER_SEED) self-host/ -o bin/jerry-compiler
	@echo "Built: bin/jerry-compiler"

# Build the jerry-test binary from cmd/jerry-test/main.jer.
# Requires a working jerry-compiler (run make build-compiler first).
build-test-runner: install-runtime install-stdlib
	JERRY_RUNTIME=$(PREFIX)/lib/jerry_runtime.a \
	JERRY_STDLIB=$(PREFIX)/share/jerry/stdlib \
		$(JERRY_COMPILER_SEED) cmd/jerry-test/ -o bin/jerry-test
	@echo "Built: bin/jerry-test"

# Build the jerry-create binary from cmd/jerry-create/main.jer.
build-create: install-runtime install-stdlib
	JERRY_RUNTIME=$(PREFIX)/lib/jerry_runtime.a \
	JERRY_STDLIB=$(PREFIX)/share/jerry/stdlib \
		$(JERRY_COMPILER_SEED) cmd/jerry-create/ -o bin/jerry-create
	@echo "Built: bin/jerry-create"

# Build the jerry-sweep binary from cmd/jerry-sweep/main.jer.
build-sweep: install-runtime install-stdlib
	JERRY_RUNTIME=$(PREFIX)/lib/jerry_runtime.a \
	JERRY_STDLIB=$(PREFIX)/share/jerry/stdlib \
		$(JERRY_COMPILER_SEED) cmd/jerry-sweep/ -o bin/jerry-sweep
	@echo "Built: bin/jerry-sweep"

# Build the jerry-main (jerry) binary from cmd/jerry-main/.
# VERSION is embedded in version.jer before compilation, then restored.
build-main: install-runtime install-stdlib
	@echo "fn jerry_version(): string { return \"$(VERSION)\"; }" > cmd/jerry-main/version.jer
	JERRY_RUNTIME=$(PREFIX)/lib/jerry_runtime.a \
	JERRY_STDLIB=$(PREFIX)/share/jerry/stdlib \
		$(JERRY_COMPILER_SEED) cmd/jerry-main/ -o bin/jerry-native
	@echo 'fn jerry_version(): string { return "dev"; }' > cmd/jerry-main/version.jer
	@echo "Built: bin/jerry-native"

# ── Run examples ──────────────────────────────────────────────────────────────

run-hello:
	$(JERRY) run examples/hello.jer

run-logging:
	$(JERRY) run examples/logging.jer

run-files:
	$(JERRY) run examples/files.jer

run-fibonacci:
	$(JERRY) run examples/fibonacci.jer

run-arrays:
	$(JERRY) run examples/arrays.jer

run-classes:
	$(JERRY) run examples/classes.jer

run-closures:
	$(JERRY) run examples/closures.jer

run-strings:
	$(JERRY) run examples/strings.jer

# Dump LLVM IR (useful for debugging codegen)
ir-hello:
	$(JERRY) ir examples/hello.jer

ir-fibonacci:
	$(JERRY) ir examples/fibonacci.jer

ir-arrays:
	$(JERRY) ir examples/arrays.jer

ir-classes:
	$(JERRY) ir examples/classes.jer

ir-strings:
	$(JERRY) ir examples/strings.jer

# ── Tests ─────────────────────────────────────────────────────────────────────

test:
	go test ./...
	$(JERRY) test tests/

# ── Build the installed binary ────────────────────────────────────────────────

install: build
	cp bin/jerry /usr/local/bin/jerry

# Pre-compile the C runtime to a static archive so jerry can find it by path
# instead of extracting the go:embed copy on every run.
# Set PREFIX to install somewhere other than /usr/local.
PREFIX ?= /usr/local
install-runtime:
	mkdir -p $(PREFIX)/lib
	$(CC) -O2 -c runtime/src/runtime.c -Iruntime/src -o /tmp/jerry_runtime.o
	ar rcs $(PREFIX)/lib/jerry_runtime.a /tmp/jerry_runtime.o
	rm /tmp/jerry_runtime.o
	@echo "Installed: $(PREFIX)/lib/jerry_runtime.a"
	@echo "To use it: export JERRY_RUNTIME=$(PREFIX)/lib/jerry_runtime.a"

install-stdlib:
	mkdir -p $(PREFIX)/share/jerry/stdlib
	cp stdlib/*.jer $(PREFIX)/share/jerry/stdlib/
	@echo "Installed: $(PREFIX)/share/jerry/stdlib/"
	@echo "To use it: export JERRY_STDLIB=$(PREFIX)/share/jerry/stdlib"

clean:
	rm -rf bin/
