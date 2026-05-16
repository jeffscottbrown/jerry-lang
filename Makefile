.PHONY: build test run-hello run-fibonacci run-arrays run-classes clean ir-hello

JERRY = go run ./cmd/jerry

build:
	go build -o bin/jerry ./cmd/jerry

# ── Run examples ──────────────────────────────────────────────────────────────

run-hello:
	$(JERRY) run examples/hello.alt

run-fibonacci:
	$(JERRY) run examples/fibonacci.alt

run-arrays:
	$(JERRY) run examples/arrays.alt

run-classes:
	$(JERRY) run examples/classes.alt

# Dump LLVM IR (useful for debugging codegen)
ir-hello:
	$(JERRY) ir examples/hello.alt

ir-fibonacci:
	$(JERRY) ir examples/fibonacci.alt

ir-arrays:
	$(JERRY) ir examples/arrays.alt

ir-classes:
	$(JERRY) ir examples/classes.alt

# ── Tests ─────────────────────────────────────────────────────────────────────

test:
	go test ./...

# ── Build the installed binary ────────────────────────────────────────────────

install: build
	cp bin/jerry /usr/local/bin/jerry

clean:
	rm -rf bin/
