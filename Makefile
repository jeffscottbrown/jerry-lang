.PHONY: build-compiler build-test-runner build-create build-sweep build-get build-lsp build-main \
        test install install-runtime install-stdlib check-deps \
        run-hello run-logging run-files run-fibonacci run-arrays run-classes run-closures run-strings \
        ir-hello ir-fibonacci ir-arrays ir-classes ir-strings \
        clean bootstrap

# ── Paths ─────────────────────────────────────────────────────────────────────

PREFIX ?= /usr/local

# Where the installed runtime and stdlib live (mirrors Homebrew layout).
RUNTIME_A  = $(PREFIX)/lib/jerry_runtime.a
STDLIB_DIR = $(PREFIX)/share/jerry/stdlib

# Seed binary used to (re)compile Jerry tools. Override if needed:
#   make build-compiler JERRY_COMPILER_SEED=/path/to/jerry-compiler
JERRY_COMPILER_SEED ?= jerry-compiler

# jerry binary used to run programs and tests. Override for a local build:
#   make test JERRY=./bin/jerry-native
JERRY ?= jerry

# jerry-compiler used when running tests.  Always the locally-built binary,
# which may support language features not yet in the installed seed.
JERRY_COMPILER ?= bin/jerry-compiler

# Version embedded in the jerry dispatcher binary.
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)

# ── Dependency checks ─────────────────────────────────────────────────────────
#
# Each $(call require,...) call aborts the current target with a clear message
# if the requested command is absent.  Checks are placed in target recipes so
# unrelated targets (e.g. `make clean`) never trigger them.

define require
@command -v $(1) >/dev/null 2>&1 || { \
	printf '\nError: "%s" not found.\n%s\n\n' "$(1)" "$(2)"; \
	exit 1; \
}
endef

CLANG_HINT   = Install clang:\n  macOS:  xcode-select --install\n  Linux:  sudo apt install clang  (or: sudo dnf install clang)
AR_HINT      = Install binutils:\n  macOS:  xcode-select --install\n  Linux:  sudo apt install binutils
JERRY_HINT   = jerry is not installed.\n  Install via Homebrew:  brew install jeffscottbrown/jerry/jerry\n  Or build it:          make build-main  (requires a seed jerry-compiler)
SEED_HINT    = jerry-compiler seed not found (looked for "$(JERRY_COMPILER_SEED)").\n  Install jerry via Homebrew to get a seed binary:\n    brew install jeffscottbrown/jerry/jerry\n  Or set JERRY_COMPILER_SEED=/path/to/jerry-compiler

# check-deps prints the status of every required tool.
check-deps:
	@printf '%-24s' "clang / cc:"; \
	  command -v clang >/dev/null 2>&1 && echo "ok ($$( command -v clang ))" \
	  || echo "MISSING — $(CLANG_HINT)"
	@printf '%-24s' "ar:"; \
	  command -v ar >/dev/null 2>&1 && echo "ok ($$( command -v ar ))" \
	  || echo "MISSING — $(AR_HINT)"
	@printf '%-24s' "$(JERRY_COMPILER_SEED):"; \
	  command -v "$(JERRY_COMPILER_SEED)" >/dev/null 2>&1 \
	    && echo "ok ($$( command -v $(JERRY_COMPILER_SEED) ))" \
	  || echo "MISSING — $(SEED_HINT)"
	@printf '%-24s' "$(JERRY):"; \
	  command -v "$(JERRY)" >/dev/null 2>&1 \
	    && echo "ok ($$( command -v $(JERRY) ))" \
	  || echo "MISSING — $(JERRY_HINT)"

# ── Runtime / stdlib installation ─────────────────────────────────────────────

STDLIB_SENTINEL = $(STDLIB_DIR)/.installed
RUNTIME_SRCS    = runtime/src/runtime.c runtime/src/runtime.h

# Real file rules — Make can track these and skip work when up to date.
$(RUNTIME_A): $(RUNTIME_SRCS)
	$(call require,clang,$(CLANG_HINT))
	$(call require,ar,$(AR_HINT))
	mkdir -p $(PREFIX)/lib
	clang -O2 -c runtime/src/runtime.c -Iruntime/src -o /tmp/jerry_runtime.o
	ar rcs $(RUNTIME_A) /tmp/jerry_runtime.o
	rm /tmp/jerry_runtime.o

$(STDLIB_SENTINEL): $(wildcard stdlib/*.jer)
	mkdir -p $(STDLIB_DIR)
	cp stdlib/*.jer $(STDLIB_DIR)/
	touch $(STDLIB_SENTINEL)

# Phony aliases for manual use.
install-runtime: $(RUNTIME_A)
	@echo "Installed: $(RUNTIME_A)"
	@echo "To use it: export JERRY_RUNTIME=$(RUNTIME_A)"

install-stdlib: $(STDLIB_SENTINEL)
	@echo "Installed: $(STDLIB_DIR)/"
	@echo "To use it: export JERRY_STDLIB=$(STDLIB_DIR)"

# ── Build Jerry tools (all require a seed jerry-compiler) ─────────────────────

SELF_HOST_SRCS = $(wildcard self-host/*.jer)

bin/jerry-compiler: $(SELF_HOST_SRCS) $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) self-host/ -o bin/jerry-compiler
	@echo "Built: bin/jerry-compiler"

build-compiler: bin/jerry-compiler

build-test-runner: $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) cmd/jerry-test/ -o bin/jerry-test
	@echo "Built: bin/jerry-test"

build-create: $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) cmd/jerry-create/ -o bin/jerry-create
	@echo "Built: bin/jerry-create"

build-sweep: $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) cmd/jerry-sweep/ -o bin/jerry-sweep
	@echo "Built: bin/jerry-sweep"

build-get: $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) cmd/jerry-get/ -o bin/jerry-get
	@echo "Built: bin/jerry-get"

build-lsp: $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) cmd/jerry-lsp/ -o bin/jerry-lsp
	@echo "Built: bin/jerry-lsp"

# Embeds VERSION into version.jer before compiling, then restores the dev default.
build-main: $(RUNTIME_A) $(STDLIB_SENTINEL)
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	@mkdir -p bin
	@echo "fn jerry_version(): string { return \"$(VERSION)\"; }" > cmd/jerry-main/version.jer
	JERRY_RUNTIME=$(RUNTIME_A) JERRY_STDLIB=$(STDLIB_DIR) \
		$(JERRY_COMPILER_SEED) cmd/jerry-main/ -o bin/jerry-native
	@echo 'fn jerry_version(): string { return "dev"; }' > cmd/jerry-main/version.jer
	@echo "Built: bin/jerry-native"

# install copies the locally-built binaries to PREFIX/bin.
install: build-main bin/jerry-compiler
	$(call require,$(JERRY_COMPILER_SEED),$(SEED_HINT))
	mkdir -p $(PREFIX)/bin
	cp bin/jerry-native   $(PREFIX)/bin/jerry
	cp bin/jerry-compiler $(PREFIX)/bin/jerry-compiler
	@echo "Installed: $(PREFIX)/bin/jerry"
	@echo "Installed: $(PREFIX)/bin/jerry-compiler"

# ── Tests ─────────────────────────────────────────────────────────────────────

test: bin/jerry-compiler
	$(call require,$(JERRY),$(JERRY_HINT))
	cc -O2 -c tests/extern_test.c -o /tmp/jerry_extern_test.o
	ar rcs /tmp/libextern_test.a /tmp/jerry_extern_test.o
	JERRY_COMPILER=$(JERRY_COMPILER) JERRY_STDLIB=$(STDLIB_DIR) JERRY_RUNTIME=$(RUNTIME_A) \
		$(JERRY) test tests/ -lextern_test -L/tmp
	rm -f /tmp/jerry_extern_test.o /tmp/libextern_test.a
	JERRY_COMPILER=$(JERRY_COMPILER) JERRY_STDLIB=$(STDLIB_DIR) JERRY_RUNTIME=$(RUNTIME_A) \
		$(JERRY) test cmd/jerry-lsp/

# ── Run examples ──────────────────────────────────────────────────────────────

run-hello:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/hello.jer

run-logging:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/logging.jer

run-files:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/files.jer

run-fibonacci:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/fibonacci.jer

run-arrays:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/arrays.jer

run-classes:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/classes.jer

run-closures:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/closures.jer

run-strings:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) run examples/strings.jer

# ── LLVM IR dumps (useful for debugging codegen) ──────────────────────────────

ir-hello:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) ir examples/hello.jer

ir-fibonacci:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) ir examples/fibonacci.jer

ir-arrays:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) ir examples/arrays.jer

ir-classes:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) ir examples/classes.jer

ir-strings:
	$(call require,$(JERRY),$(JERRY_HINT))
	$(JERRY) ir examples/strings.jer

# ── Cleanup ────────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/
