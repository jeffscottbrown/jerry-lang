// Command jerry: Jerry language compiler.
//
// Usage:
//
//	jerry compile <file.alt> [-o output]   compile to native binary
//	jerry run     <file.alt>               compile and run immediately
//	jerry ir      <file.alt>               dump LLVM IR to stdout
package main

import (
	"jerry/internal/checker"
	"jerry/internal/codegen"
	"jerry/internal/parser"
	"fmt"
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
	src := os.Args[2]

	switch cmd {
	case "compile":
		out := ""
		for i := 3; i < len(os.Args)-1; i++ {
			if os.Args[i] == "-o" {
				out = os.Args[i+1]
			}
		}
		if out == "" {
			out = strings.TrimSuffix(src, filepath.Ext(src))
		}
		if err := compile(src, out, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "run":
		tmp, err := os.MkdirTemp("", "jerry-run-*")
		if err != nil {
			fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmp)
		bin := filepath.Join(tmp, "a.out")
		if err := compile(src, bin, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		runCmd := exec.Command(bin, os.Args[3:]...)
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
		ir, err := compileToIR(src)
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

func compile(src, outBin string, _ bool) error {
	ir, err := compileToIR(src)
	if err != nil {
		return err
	}

	// Write IR to a temp file.
	tmp, err := os.CreateTemp("", "jerry-*.ll")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(ir); err != nil {
		return fmt.Errorf("failed to write IR: %w", err)
	}
	tmp.Close()

	// Find the runtime library.
	runtimeLib, err := findRuntime()
	if err != nil {
		return err
	}

	// Find the sysroot so clang can locate system headers on macOS.
	sysroot, _ := exec.Command("xcrun", "--show-sdk-path").Output()

	// Compile with clang: IR + runtime C → native binary.
	args := []string{
		"-O1",
		tmp.Name(),
		runtimeLib,
		"-o", outBin,
		"-lm",
	}
	if len(sysroot) > 0 {
		args = append(args, "-isysroot", strings.TrimSpace(string(sysroot)))
	}
	out, err := exec.Command("clang", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("clang error:\n%s", out)
	}
	return nil
}

func compileToIR(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", src, err)
	}

	prog, err := parser.Parse(src, string(data))
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

// findRuntime locates runtime/runtime.c relative to the executable or GOPATH.
func findRuntime() (string, error) {
	// Try next to the executable.
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "..", "runtime", "runtime.c")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	// Try relative to cwd.
	for _, rel := range []string{"runtime/runtime.c", "../runtime/runtime.c"} {
		if _, err := os.Stat(rel); err == nil {
			abs, _ := filepath.Abs(rel)
			return abs, nil
		}
	}
	return "", fmt.Errorf("cannot find runtime/runtime.c — run jerry from project root or install properly")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jerry: "+format+"\n", args...)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  jerry compile <file.alt> [-o output]   compile to native binary
  jerry run     <file.alt>               compile and run
  jerry ir      <file.alt>               dump LLVM IR`)
}
