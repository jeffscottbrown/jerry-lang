## examples/linking — build a Jerry program that calls C and NASM functions.
##
## Usage:
##   make        build ./hello
##   make run    build and run
##   make clean  remove build artifacts

JERRY_COMPILER ?= jerry-compiler

OS := $(shell uname -s)
ifeq ($(OS),Darwin)
    ASM_FMT  := macho64
    ASM_DEFS := -DMACHO
else
    ASM_FMT  := elf64
    ASM_DEFS :=
endif

ASM_OBJ := src/asm/hello.o
C_OBJ   := src/c/hello.o
LIB     := libhello.a
BIN     := hello

.PHONY: all run clean

all: $(BIN)

$(ASM_OBJ): src/asm/hello.asm
	nasm -f $(ASM_FMT) $(ASM_DEFS) $< -o $@

$(C_OBJ): src/c/hello.c
	cc -O2 -c $< -o $@

$(LIB): $(ASM_OBJ) $(C_OBJ)
	ar rcs $@ $^

$(BIN): $(LIB) src/jerry/main.jer
	$(JERRY_COMPILER) src/jerry/main.jer -o $(BIN) -lhello -L.

run: $(BIN)
	./$(BIN)

clean:
	rm -f $(ASM_OBJ) $(C_OBJ) $(LIB) $(BIN)
