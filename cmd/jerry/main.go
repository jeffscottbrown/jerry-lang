// Command jerry: Jerry language compiler.
//
// Usage:
//
//	jerry compile <file.jer> [file.jer ...] [-o output]   compile to native binary
//	jerry run     <file.jer> [file.jer ...]               compile and run immediately
//	jerry ir      <file.jer> [file.jer ...]               dump LLVM IR to stdout
//
// Each source file is parsed independently. Functions defined in any project
// file are visible to all other project files. Stdlib modules must be explicitly
// included with `include @modulename` and are only visible in files that include
// them. stdlib/core.jer is always included automatically.
package main

import (
	"fmt"
	"jerry/internal/ast"
	"jerry/internal/checker"
	"jerry/internal/codegen"
	"jerry/internal/parser"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]

	switch cmd {
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

	runtimeLib, err := findRuntime()
	if err != nil {
		return err
	}

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
	stdlibDir, err := findStdlib()
	if err != nil {
		return "", err
	}

	// Always load core.jer.
	coreAST, err := parseStdlibFile(stdlibDir, "core")
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
			stdAST, err := parseStdlibFile(stdlibDir, name)
			if err != nil {
				return "", fmt.Errorf("%s: unknown stdlib module @%s (no file %s/%s.jer)",
					srcs[i], name, stdlibDir, name)
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

func parseStdlibFile(stdlibDir, name string) (*ast.Program, error) {
	path := filepath.Join(stdlibDir, name+".jer")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parser.Parse(path, string(data))
}

func findStdlib() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "..", "stdlib")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Abs(candidate)
		}
	}
	for _, rel := range []string{"stdlib", "../stdlib"} {
		if _, err := os.Stat(rel); err == nil {
			return filepath.Abs(rel)
		}
	}
	return "", fmt.Errorf("cannot find stdlib directory — run jerry from the project root")
}

func findRuntime() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "..", "runtime", "runtime.c")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	for _, rel := range []string{"runtime/runtime.c", "../runtime/runtime.c"} {
		if _, err := os.Stat(rel); err == nil {
			abs, _ := filepath.Abs(rel)
			return abs, nil
		}
	}
	return "", fmt.Errorf("cannot find runtime/runtime.c — run jerry from the project root")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jerry: "+format+"\n", args...)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  jerry compile <file.jer> [file.jer ...] [-o output]   compile to binary
  jerry run     <file.jer> [file.jer ...]               compile and run
  jerry ir      <file.jer> [file.jer ...]               dump LLVM IR`)
}
