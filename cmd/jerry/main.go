// Command jerry: Jerry language compiler.
//
// Usage:
//
//	jerry compile <file.jer> [file.jer ...] [-o output]   compile to native binary
//	jerry run     <file.jer> [file.jer ...]               compile and run immediately
//	jerry ir      <file.jer> [file.jer ...]               dump LLVM IR to stdout
//	jerry -v | --version                                  print version and exit
//
// Each source file is parsed independently. Functions defined in any project
// file are visible to all other project files. Stdlib modules must be explicitly
// included with `include @modulename` and are only visible in files that include
// them. stdlib/core.jer is always included automatically.
package main

import (
	"fmt"
	"io/fs"
	"jerry/internal/ast"
	"jerry/internal/checker"
	"jerry/internal/codegen"
	"jerry/internal/parser"
	jerryruntime "jerry/runtime"
	"jerry/stdlib"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Version is injected at build time via -ldflags "-X main.Version=v1.2.3".
// Falls back to "dev" for local builds.
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]

	switch cmd {
	case "-v", "--version":
		fmt.Println("jerry " + Version)
		return
	case "compile":
		outBin, srcs := parseCompileArgs(os.Args[2:])
		if len(srcs) == 0 {
			fatalf("no source files given")
		}
		if outBin == "" {
			outBin = strings.TrimSuffix(srcs[0], filepath.Ext(srcs[0]))
		}
		if err := compile(srcs, outBin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "run":
		srcs, rest := splitSrcs(os.Args[2:])
		if len(srcs) == 0 {
			fatalf("no source files given")
		}
		tmp, err := os.MkdirTemp("", "jerry-run-*")
		if err != nil {
			fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmp)
		bin := filepath.Join(tmp, "a.out")
		if err := compile(srcs, bin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		runCmd := exec.Command(bin, rest...)
		runCmd.Stdin = os.Stdin
		runCmd.Stdout = os.Stdout
		runCmd.Stderr = os.Stderr
		if err := runCmd.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			fatalf("run failed: %v", err)
		}

	case "ir":
		srcs, _ := splitSrcs(os.Args[2:])
		if len(srcs) == 0 {
			fatalf("no source files given")
		}
		ir, err := compileToIR(srcs)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(ir)

	default:
		// Catch the old "jerry <file.jer>" shorthand with no subcommand.
		if len(os.Args) < 3 {
			usage()
			os.Exit(1)
		}
		usage()
		os.Exit(1)
	}
}

func parseCompileArgs(args []string) (outBin string, srcs []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" && i+1 < len(args) {
			outBin = args[i+1]
			i++
		} else {
			srcs = append(srcs, args[i])
		}
	}
	return
}

func splitSrcs(args []string) (srcs, rest []string) {
	i := 0
	for i < len(args) && (strings.HasSuffix(args[i], ".jer") || strings.HasSuffix(args[i], ".jl")) {
		srcs = append(srcs, args[i])
		i++
	}
	rest = args[i:]
	return
}

func compile(srcs []string, outBin string) error {
	ir, err := compileToIR(srcs)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "jerry-*.ll")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(ir); err != nil {
		return fmt.Errorf("failed to write IR: %w", err)
	}
	tmp.Close()

	runtimeLib, cleanupRuntime, err := extractRuntime()
	if err != nil {
		return err
	}
	defer cleanupRuntime()

	sysroot, _ := exec.Command("xcrun", "--show-sdk-path").Output()
	args := []string{"-O1", tmp.Name(), runtimeLib, "-o", outBin, "-lm"}
	if len(sysroot) > 0 {
		args = append(args, "-isysroot", strings.TrimSpace(string(sysroot)))
	}
	out, err := exec.Command("clang", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("clang error:\n%s", out)
	}
	return nil
}

// compileToIR is the main compilation pipeline:
//  1. Parse each project file independently.
//  2. Parse stdlib/core.jer (always included).
//  3. Parse any stdlib modules referenced by `include @name` declarations.
//  4. Type-check all files with per-file scoping via checker.CheckAll.
//  5. Generate LLVM IR from all ASTs together.
func compileToIR(srcs []string) (string, error) {
	fsys := stdlibFS()

	// Always load core.jer.
	coreAST, err := parseStdlibFile(fsys, "core")
	if err != nil {
		return "", fmt.Errorf("failed to load stdlib/core.jer: %w", err)
	}

	// Parse each project source file independently.
	projectASTs := make([]*ast.Program, 0, len(srcs))
	for _, src := range srcs {
		data, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", src, err)
		}
		prog, err := parser.Parse(src, string(data))
		if err != nil {
			return "", fmt.Errorf("parse error in %s: %w", src, err)
		}
		projectASTs = append(projectASTs, prog)
	}

	// Collect all unique stdlib includes across all project files and load them.
	stdlibASTs := make(map[string]*ast.Program)
	for i, prog := range projectASTs {
		for _, tl := range prog.Stmts {
			if tl.Include == nil {
				continue
			}
			name := tl.Include.Stdlib
			if name == "core" {
				return "", fmt.Errorf("%s: do not include @core — it is always available", srcs[i])
			}
			if _, already := stdlibASTs[name]; already {
				continue
			}
			stdAST, err := parseStdlibFile(fsys, name)
			if err != nil {
				return "", fmt.Errorf("%s: unknown stdlib module @%s", srcs[i], name)
			}
			stdlibASTs[name] = stdAST
		}
	}

	// Type-check with per-file scoping.
	info, errs := checker.CheckAll(projectASTs, coreAST, stdlibASTs)
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return "", fmt.Errorf("type errors:\n%s", strings.Join(msgs, "\n"))
	}

	// Assemble ordered list of ASTs for codegen:
	//   core → included stdlibs (alphabetical) → project files (in argument order)
	allASTs := []*ast.Program{coreAST}
	var stdlibNames []string
	for name := range stdlibASTs {
		stdlibNames = append(stdlibNames, name)
	}
	sort.Strings(stdlibNames)
	for _, name := range stdlibNames {
		allASTs = append(allASTs, stdlibASTs[name])
	}
	allASTs = append(allASTs, projectASTs...)

	ir, err := codegen.Generate(allASTs, info)
	if err != nil {
		return "", fmt.Errorf("codegen error: %w", err)
	}
	return ir, nil
}

func parseStdlibFile(fsys fs.FS, name string) (*ast.Program, error) {
	filename := name + ".jer"
	data, err := fs.ReadFile(fsys, filename)
	if err != nil {
		return nil, err
	}
	return parser.Parse("stdlib/"+filename, string(data))
}

// stdlibFS returns the filesystem to use for stdlib lookups.
// If the JERRY_STDLIB environment variable points to a directory, an OS-based
// FS rooted there is returned — useful when developing the stdlib itself.
// Otherwise the embedded FS baked into the binary is used.
func stdlibFS() fs.FS {
	if dir := os.Getenv("JERRY_STDLIB"); dir != "" {
		return os.DirFS(dir)
	}
	return stdlib.Files
}

// extractRuntime writes the embedded runtime C sources to a temp directory
// and returns the path to runtime.c. The caller is responsible for removing
// the directory when done.
func extractRuntime() (runtimeC string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "jerry-runtime-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create runtime temp dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }

	for _, name := range []string{"runtime.c", "runtime.h"} {
		data, err := fs.ReadFile(jerryruntime.Files, "src/"+name)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("embedded runtime missing %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("failed to write runtime %s: %w", name, err)
		}
	}
	return filepath.Join(dir, "runtime.c"), cleanup, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jerry: "+format+"\n", args...)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  jerry compile <file.jer> [file.jer ...] [-o output]   compile to binary
  jerry run     <file.jer> [file.jer ...]               compile and run
  jerry ir      <file.jer> [file.jer ...]               dump LLVM IR
  jerry -v | --version                                  print version`)
}
