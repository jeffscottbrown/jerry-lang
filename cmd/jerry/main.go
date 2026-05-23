// Command jerry: Jerry language compiler.
//
// Usage:
//
//	jerry compile <file.jer|dir> [...]  [-o output]   compile to native binary
//	jerry run     <file.jer|dir> [...]                compile and run immediately
//	jerry ir      <file.jer|dir> [...]                dump LLVM IR to stdout
//	jerry test    [dir|file_test.jer ...]             run unit tests
//	jerry get     <module>@<version>                      fetch a remote module
//	jerry sweep                                            sync jerry.remotes and jerry.sum
//	jerry -v | --version                                  print version and exit
//
// Each source file is parsed independently. Functions defined in any project
// file are visible to all other project files. Stdlib modules must be explicitly
// included with `include @modulename` and are only visible in files that include
// them. Remote modules require `include "github.com/..."` and a jerry.remotes entry.
// stdlib/core.jer is always included automatically.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

)

// Version is injected at build time via -ldflags "-X main.Version=v1.2.3"
// for release builds. For binaries installed via `go install`, it falls back
// to the module version embedded automatically in the binary's build info.
// Local `go run` builds show "dev".
var Version = "dev"

func init() {
	if Version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			Version = info.Main.Version
		}
	}
}

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

	case "lsp":
		runJerryTool("jerry-lsp", os.Args[2:])

	case "compile":
		if len(os.Args) < 3 {
			fatalf("no source files given")
		}
		runJerryCompiler(os.Args[2:])

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
		runJerryCompiler(append(srcs, "-o", bin))
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
		if len(os.Args) < 3 {
			fatalf("no source files given")
		}
		runJerryCompiler(append(os.Args[2:], "--ir"))

	case "get":
		if len(os.Args) < 3 {
			fatalf("usage: jerry get <module>@<version>")
		}
		runJerryTool("jerry-get", os.Args[2:])

	case "sweep":
		runJerryTool("jerry-sweep", os.Args[2:])

	case "test":
		testArgs := os.Args[2:]
		if len(testArgs) == 0 {
			testArgs = []string{"."}
		}
		runJerryTool("jerry-test", testArgs)

	case "create":
		runJerryTool("jerry-create", os.Args[2:])

	default:
		usage()
		os.Exit(1)
	}
}


// ── Helpers ───────────────────────────────────────────────────────────────────

func splitSrcs(args []string) (srcs, rest []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasSuffix(a, ".jer") || strings.HasSuffix(a, ".jl") {
			srcs = append(srcs, a)
			i++
			continue
		}
		// Directory: expand to all .jer files inside it.
		if info, err := os.Stat(a); err == nil && info.IsDir() {
			entries, err := os.ReadDir(a)
			if err == nil {
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".jer") {
						srcs = append(srcs, filepath.Join(a, e.Name()))
					}
				}
			}
			i++
			continue
		}
		break
	}
	rest = args[i:]
	return
}


// ── Jerry tool discovery and invocation ──────────────────────────────────────

// runJerryCompiler invokes jerry-compiler with args, inheriting stdio.
// Exits the process if jerry-compiler is not found or exits non-zero.
func runJerryCompiler(args []string) {
	runJerryTool("jerry-compiler", args)
}

// runJerryTool finds and runs a Jerry tool binary (e.g. jerry-compiler,
// jerry-test), inheriting stdio. Exits if the binary is not found or
// exits non-zero.
func runJerryTool(name string, args []string) {
	bin, err := findJerryTool(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jerry: %s not found; run `make install` or set %s\n",
			name, strings.ToUpper(strings.ReplaceAll(name, "-", "_")))
		os.Exit(1)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "jerry: %v\n", err)
		os.Exit(1)
	}
}

// findJerryTool returns the path to a Jerry tool binary by name.
// Search order: <NAME_UPPER> env var → beside this binary → PATH.
func findJerryTool(name string) (string, error) {
	envKey := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if p := os.Getenv(envKey); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found", name)
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
  jerry test    [dir|file_test.jer ...]                 run unit tests
  jerry get     <module>@<version>                      fetch a remote module
  jerry sweep                                            sync jerry.remotes / jerry.sum
  jerry create  [--git] [--tap <owner>] <name>          scaffold a new project
  jerry -v | --version                                  print version`)
}
