.PHONY: build test run-hello run-fibonacci run-arrays run-classes run-closures run-strings clean

JERRY = go run ./cmd/jerry

build:
	go build -o bin/jerry ./cmd/jerry

# ── Run examples ──────────────────────────────────────────────────────────────

run-hello:
	$(JERRY) run examples/hello.jer

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

# ── Build the installed binary ────────────────────────────────────────────────

install: build
	cp bin/jerry /usr/local/bin/jerry

clean:
	rm -rf bin/
