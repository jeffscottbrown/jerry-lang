// Command jerry: Jerry language compiler.
//
// Usage:
//
//	jerry compile <file.jer> [file.jer ...] [-o output]   compile to native binary
//	jerry run     <file.jer> [file.jer ...]               compile and run immediately
//	jerry ir      <file.jer> [file.jer ...]               dump LLVM IR to stdout
//
// Multiple source files are concatenated before parsing, so all top-level
// declarations share a single global namespace.
package main

import (
	"fmt"
	"jerry/internal/checker"
	"jerry/internal/codegen"
	"jerry/internal/parser"
	"os"
	"os/exec"
	"path/filepath"
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
		// Collect source files (all .jer args before any program args).
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

// parseCompileArgs splits "file... [-o out]" into (outBin, srcs).
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

// splitSrcs separates .jer source files from remaining program arguments.
// Source files are any args that end in .jer; everything after the first
// non-.jer arg is treated as program arguments.
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

// compileToIR reads and concatenates all source files, then compiles to LLVM IR.
func compileToIR(srcs []string) (string, error) {
	var combined strings.Builder
	for _, src := range srcs {
		data, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", src, err)
		}
		combined.WriteString(string(data))
		combined.WriteByte('\n')
	}

	// Use the first filename for error reporting.
	name := srcs[0]
	if len(srcs) > 1 {
		name = strings.Join(srcs, "+")
	}

	prog, err := parser.Parse(name, combined.String())
	if err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	info, errs := checker.Check(prog)
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return "", fmt.Errorf("type errors:\n%s", strings.Join(msgs, "\n"))
	}

	ir, err := codegen.Generate(prog, info)
	if err != nil {
		return "", fmt.Errorf("codegen error: %w", err)
	}
	return ir, nil
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
