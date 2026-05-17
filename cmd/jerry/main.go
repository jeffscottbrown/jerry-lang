// Command jerry: Jerry language compiler.
//
// Usage:
//
//	jerry compile <file.jer> [file.jer ...] [-o output]   compile to native binary
//	jerry run     <file.jer> [file.jer ...]               compile and run immediately
//	jerry ir      <file.jer> [file.jer ...]               dump LLVM IR to stdout
//	jerry test    [dir|file_test.jer ...]                 run unit tests
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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
	"github.com/jeffscottbrown/jerry-lang/internal/codegen"
	"github.com/jeffscottbrown/jerry-lang/internal/modfile"
	"github.com/jeffscottbrown/jerry-lang/internal/module"
	"github.com/jeffscottbrown/jerry-lang/internal/parser"
	jerryruntime "github.com/jeffscottbrown/jerry-lang/runtime"
	"github.com/jeffscottbrown/jerry-lang/stdlib"
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
		runLsp()

	case "compile":
		outBin, target, srcs := parseCompileArgs(os.Args[2:])
		if len(srcs) == 0 {
			fatalf("no source files given")
		}
		if outBin == "" {
			outBin = strings.TrimSuffix(srcs[0], filepath.Ext(srcs[0]))
		}
		if err := compile(srcs, outBin, target); err != nil {
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
		if err := compile(srcs, bin, ""); err != nil {
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

	case "get":
		if len(os.Args) < 3 {
			fatalf("usage: jerry get <module>@<version>")
		}
		if err := cmdGet(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "sweep":
		if err := cmdSweep(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "test":
		if err := cmdTest(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "create":
		if err := cmdCreate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	default:
		usage()
		os.Exit(1)
	}
}

// ── jerry get ────────────────────────────────────────────────────────────────

func cmdGet(arg string) error {
	modPath, version, ok := splitAtVersion(arg)
	if !ok {
		return fmt.Errorf("jerry get: expected <module>@<version>, got %q", arg)
	}

	// Fetch (or re-use cached) module.
	_, hash, err := module.Fetch(modPath, version)
	if err != nil {
		return err
	}

	// Update jerry.remotes.
	mf, err := modfile.Parse(modfile.RemotesFileName)
	if err != nil {
		return fmt.Errorf("reading jerry.remotes: %w", err)
	}
	mf.Requires[modPath] = version
	if err := modfile.Write(modfile.RemotesFileName, mf); err != nil {
		return fmt.Errorf("writing jerry.remotes: %w", err)
	}

	// Update jerry.sum.
	sums, err := modfile.ParseSum(modfile.SumFileName)
	if err != nil {
		return fmt.Errorf("reading jerry.sum: %w", err)
	}
	sums[sums.Key(modPath, version)] = hash
	if err := sums.Write(modfile.SumFileName); err != nil {
		return fmt.Errorf("writing jerry.sum: %w", err)
	}

	fmt.Fprintf(os.Stderr, "jerry: added %s %s\n", modPath, version)
	return nil
}

// ── jerry sweep ──────────────────────────────────────────────────────────────

func cmdSweep() error {
	// Find all .jer files in the current directory.
	entries, err := os.ReadDir(".")
	if err != nil {
		return err
	}
	var srcs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jer") {
			srcs = append(srcs, e.Name())
		}
	}

	// Collect remote imports referenced in project files.
	referenced := map[string]bool{}
	for _, src := range srcs {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		prog, err := parser.Parse(src, string(data))
		if err != nil {
			continue // ignore parse errors during tidy
		}
		for _, tl := range prog.Stmts {
			if tl.Include != nil && tl.Include.Remote != "" {
				referenced[tl.Include.Remote] = true
			}
		}
	}

	mf, err := modfile.Parse(modfile.RemotesFileName)
	if err != nil {
		return fmt.Errorf("reading jerry.remotes: %w", err)
	}

	sums, err := modfile.ParseSum(modfile.SumFileName)
	if err != nil {
		return fmt.Errorf("reading jerry.sum: %w", err)
	}

	// Add missing requires and ensure hashes exist.
	changed := false
	for modPath := range referenced {
		if _, has := mf.Requires[modPath]; !has {
			return fmt.Errorf("jerry sweep: %q is included but not in jerry.remotes — run: jerry get %s@<version>", modPath, modPath)
		}
		version := mf.Requires[modPath]
		key := sums.Key(modPath, version)
		if _, has := sums[key]; !has {
			_, hash, fetchErr := module.Fetch(modPath, version)
			if fetchErr != nil {
				return fetchErr
			}
			sums[key] = hash
			changed = true
		}
	}

	// Remove requires no longer referenced.
	for modPath := range mf.Requires {
		if !referenced[modPath] {
			delete(mf.Requires, modPath)
			delete(sums, sums.Key(modPath, mf.Requires[modPath]))
			changed = true
		}
	}

	if changed {
		if err := modfile.Write(modfile.RemotesFileName, mf); err != nil {
			return fmt.Errorf("writing jerry.remotes: %w", err)
		}
		if err := sums.Write(modfile.SumFileName); err != nil {
			return fmt.Errorf("writing jerry.sum: %w", err)
		}
	}
	fmt.Fprintln(os.Stderr, "jerry: sweep complete")
	return nil
}

// ── Compilation pipeline ─────────────────────────────────────────────────────

func compile(srcs []string, outBin, target string) error {
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
	if target != "" {
		args = append(args, "-target", target)
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

// compileToIR is the main compilation pipeline:
//  1. Parse stdlib/core.jer (always included).
//  2. Parse each project file independently.
//  3. Resolve include @stdlib and include "remote" declarations.
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

	// Resolve stdlib includes.
	stdlibASTs := make(map[string]*ast.Program)
	for i, prog := range projectASTs {
		for _, tl := range prog.Stmts {
			if tl.Include == nil || tl.Include.Stdlib == "" {
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

	// Resolve remote includes via jerry.remotes / jerry.sum.
	remoteASTs, err := resolveRemoteIncludes(srcs, projectASTs)
	if err != nil {
		return "", err
	}

	// Type-check with per-file scoping.
	info, errs := checker.CheckAll(projectASTs, coreAST, stdlibASTs, remoteASTs)
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return "", fmt.Errorf("type errors:\n%s", strings.Join(msgs, "\n"))
	}

	// Assemble ordered list of ASTs for codegen:
	//   core → stdlib (alpha) → remote modules (alpha) → project files (argument order)
	allASTs := []*ast.Program{coreAST}
	for _, name := range sortedMapKeys(stdlibASTs) {
		allASTs = append(allASTs, stdlibASTs[name])
	}
	for _, path := range sortedMapKeys(remoteASTs) {
		allASTs = append(allASTs, remoteASTs[path]...)
	}
	allASTs = append(allASTs, projectASTs...)

	ir, err := codegen.Generate(allASTs, info)
	if err != nil {
		return "", fmt.Errorf("codegen error: %w", err)
	}
	return ir, nil
}

// resolveRemoteIncludes loads all remote modules referenced across the project
// files. It reads jerry.remotes for version pinning and jerry.sum for verification,
// then loads .jer files from the local cache.
func resolveRemoteIncludes(srcs []string, projectASTs []*ast.Program) (map[string][]*ast.Program, error) {
	// Collect all unique remote import paths.
	type importSite struct {
		srcIdx int
		path   string
	}
	var imports []importSite
	seen := map[string]bool{}
	for i, prog := range projectASTs {
		for _, tl := range prog.Stmts {
			if tl.Include == nil || tl.Include.Remote == "" {
				continue
			}
			path := tl.Include.Remote
			if !seen[path] {
				seen[path] = true
				imports = append(imports, importSite{i, path})
			}
		}
	}
	if len(imports) == 0 {
		return map[string][]*ast.Program{}, nil
	}

	// Resolve jerry.remotes and jerry.sum relative to the source files.
	projectDir := filepath.Dir(srcs[0])

	// Load jerry.remotes for version info.
	mf, err := modfile.Parse(filepath.Join(projectDir, modfile.RemotesFileName))
	if err != nil {
		return nil, fmt.Errorf("reading jerry.remotes: %w", err)
	}

	// Load jerry.sum for hash verification.
	sums, err := modfile.ParseSum(filepath.Join(projectDir, modfile.SumFileName))
	if err != nil {
		return nil, fmt.Errorf("reading jerry.sum: %w", err)
	}

	result := make(map[string][]*ast.Program)

	for _, imp := range imports {
		modPath := imp.path
		version, ok := mf.Requires[modPath]
		if !ok {
			return nil, fmt.Errorf("%s: module %q is included but not in jerry.remotes\n  run: jerry get %s@<version>",
				srcs[imp.srcIdx], modPath, modPath)
		}

		// Fetch the module if not already cached.
		cacheDir, actualHash, err := module.Fetch(modPath, version)
		if err != nil {
			return nil, fmt.Errorf("fetching module %s@%s: %w", modPath, version, err)
		}

		// Verify hash against jerry.sum if an entry exists.
		key := sums.Key(modPath, version)
		if expectedHash, has := sums[key]; has && expectedHash != actualHash {
			return nil, fmt.Errorf("hash mismatch for %s@%s: expected %s, got %s",
				modPath, version, expectedHash, actualHash)
		}

		// Parse all .jer files in the cached module.
		jerFiles, err := module.JerFiles(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("reading cached module %s@%s: %w", modPath, version, err)
		}
		if len(jerFiles) == 0 {
			return nil, fmt.Errorf("module %s@%s contains no .jer files", modPath, version)
		}

		var progs []*ast.Program
		for _, f := range jerFiles {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			prog, err := parser.Parse(f, string(data))
			if err != nil {
				return nil, fmt.Errorf("parse error in %s@%s (%s): %w", modPath, version, filepath.Base(f), err)
			}
			progs = append(progs, prog)
		}
		result[modPath] = progs
	}

	return result, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func parseCompileArgs(args []string) (outBin, target string, srcs []string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-o" && i+1 < len(args):
			outBin = args[i+1]
			i++
		case args[i] == "--target" && i+1 < len(args):
			target = args[i+1]
			i++
		default:
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

// splitAtVersion splits "github.com/x/y@v1.0.0" into ("github.com/x/y", "v1.0.0", true).
func splitAtVersion(arg string) (modPath, version string, ok bool) {
	idx := strings.LastIndex(arg, "@")
	if idx < 0 {
		return "", "", false
	}
	return arg[:idx], arg[idx+1:], true
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// ── jerry test ───────────────────────────────────────────────────────────────

// cmdTest discovers *_test.jer files, finds all fn test_*() functions in them,
// generates a temporary main file that calls each one, compiles everything
// together, and runs the resulting binary.
func cmdTest(args []string) error {
	// Collect test files: explicit .jer args, directory args (scanned for
	// *_test.jer), or fall back to globbing *_test.jer in cwd.
	var testFiles []string
	for _, a := range args {
		if strings.HasSuffix(a, ".jer") {
			testFiles = append(testFiles, a)
		} else {
			// Treat as a directory — scan it for *_test.jer files.
			entries, err := os.ReadDir(a)
			if err != nil {
				return fmt.Errorf("jerry test: %w", err)
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.jer") {
					testFiles = append(testFiles, filepath.Join(a, e.Name()))
				}
			}
		}
	}
	if len(testFiles) == 0 {
		entries, err := os.ReadDir(".")
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.jer") {
				testFiles = append(testFiles, e.Name())
			}
		}
	}
	if len(testFiles) == 0 {
		fmt.Fprintln(os.Stderr, "jerry test: no test files found")
		return nil
	}

	// Parse each test file and collect test function names per file.
	type fileTests struct {
		path  string
		names []string
	}
	var allTests []fileTests
	for _, path := range testFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", path, err)
		}
		prog, err := parser.Parse(path, string(data))
		if err != nil {
			return fmt.Errorf("parse error in %s: %w", path, err)
		}
		var names []string
		for _, tl := range prog.Stmts {
			if tl.FnDecl == nil {
				continue
			}
			fn := tl.FnDecl
			if strings.HasPrefix(fn.Name, "test_") && len(fn.Params) == 0 {
				names = append(names, fn.Name)
			}
		}
		if len(names) > 0 {
			allTests = append(allTests, fileTests{path, names})
		}
	}
	if len(allTests) == 0 {
		fmt.Fprintln(os.Stderr, "jerry test: no test_* functions found")
		return nil
	}

	// Generate the test main file.
	var sb strings.Builder
	sb.WriteString("include @testing\n\nfn main(): void {\n")
	for _, ft := range allTests {
		fmt.Fprintf(&sb, "    print(\"--- %s ---\");\n", ft.path)
		for _, name := range ft.names {
			fmt.Fprintf(&sb, "    print(\"  %s\");\n", name)
			fmt.Fprintf(&sb, "    %s();\n", name)
		}
	}
	sb.WriteString("    test_summary();\n}\n")

	// Write to a temp file.
	tmp, err := os.CreateTemp("", "jerry-test-main-*.jer")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(sb.String()); err != nil {
		return err
	}
	tmp.Close()

	// Compile test files + generated main.
	srcs := append(testFiles, tmp.Name())
	tmpDir, err := os.MkdirTemp("", "jerry-test-bin-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	bin := filepath.Join(tmpDir, "test")

	if err := compile(srcs, bin, ""); err != nil {
		return err
	}

	// Run.
	runCmd := exec.Command(bin)
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	if err := runCmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		return fmt.Errorf("test run failed: %w", err)
	}
	return nil
}

// ── jerry create ─────────────────────────────────────────────────────────────

func cmdCreate(args []string) error {
	withGit := false
	tapOwner := ""
	var name string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--git":
			withGit = true
		case "--tap":
			if i+1 >= len(args) {
				return fmt.Errorf("--tap requires a GitHub username")
			}
			i++
			tapOwner = args[i]
			withGit = true // --tap implies --git
		default:
			if name == "" {
				name = args[i]
			}
		}
	}
	if name == "" {
		return fmt.Errorf("usage: jerry create [--git] [--tap <github-owner>] <project-name>")
	}

	tapDir := "homebrew-" + name

	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("directory %q already exists", name)
	}
	if tapOwner != "" {
		if _, err := os.Stat(tapDir); err == nil {
			return fmt.Errorf("directory %q already exists", tapDir)
		}
	}

	writeIn := func(root, rel, content string) error {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0644)
	}

	// ── Project files ────────────────────────────────────────────────────────
	for rel, content := range map[string]string{
		"main.jer":      createMainJer(name),
		"jerry.remotes": createRemotes(),
		"Makefile":      createMakefile(name),
		"README.md":     createProjectReadme(name, tapOwner),
	} {
		if err := writeIn(name, rel, content); err != nil {
			return err
		}
	}
	if withGit {
		if err := writeIn(name, ".github/workflows/release.yml", createWorkflow(name, tapOwner)); err != nil {
			return err
		}
	}

	// ── Homebrew tap files ───────────────────────────────────────────────────
	if tapOwner != "" {
		if err := writeIn(tapDir, "Formula/"+name+".rb", createFormula(name, tapOwner)); err != nil {
			return err
		}
		if err := writeIn(tapDir, "README.md", createTapReadme(name, tapOwner)); err != nil {
			return err
		}
	}

	// ── Summary ──────────────────────────────────────────────────────────────
	fmt.Fprintf(os.Stderr, "jerry: created project %q\n", name)
	if withGit {
		fmt.Fprintf(os.Stderr, "jerry: GitHub Actions workflow written to %s/.github/workflows/release.yml\n", name)
	}
	if tapOwner != "" {
		fmt.Fprintf(os.Stderr, "jerry: Homebrew tap written to %s/\n", tapDir)
		fmt.Fprintf(os.Stderr, "       Push it to github.com/%s/%s and add HOMEBREW_TAP_TOKEN to %s/%s secrets.\n",
			tapOwner, tapDir, tapOwner, name)
	}
	fmt.Fprintf(os.Stderr, "\nGet started:\n  cd %s\n  jerry run main.jer\n", name)
	return nil
}

func createMainJer(name string) string {
	return fmt.Sprintf(`include "github.com/jeffscottbrown/jerry-string"

fn main() {
    // Split a sentence into words and print each one.
    let sentence: string = "Welcome to %s";
    let words: string[] = split_whitespace(sentence);

    print("Project: %s");
    print("Words in greeting: " + int_to_string(len(words)));

    let i: int = 0;
    while i < len(words) {
        print("  [" + int_to_string(i) + "] " + words[i]);
        i = i + 1;
    }

    // Join them back together with a separator.
    print("Rejoined: " + join(words, "-"));
}
`, name, name)
}

func createRemotes() string {
	return "github.com/jeffscottbrown/jerry-string v0.0.1\n"
}

func createMakefile(name string) string {
	// Makefile requires tabs for recipe indentation.
	return fmt.Sprintf(".PHONY: run build clean\n\nBINARY := %s\n\nrun:\n\tjerry run main.jer\n\nbuild:\n\tjerry compile main.jer -o $(BINARY)\n\nclean:\n\trm -f $(BINARY)\n", name)
}

func createProjectReadme(name, tapOwner string) string {
	installSection := ""
	if tapOwner != "" {
		installSection = fmt.Sprintf(`
## Install via Homebrew

`+"```"+`sh
brew tap %s/%s
brew install %s
`+"```"+`
`, tapOwner, name, name)
	}
	return fmt.Sprintf(`# %s

A Jerry language project.
%s
## Build from source

Requires the [Jerry compiler](https://github.com/jeffscottbrown/jerry-lang) and clang.

- macOS: `+"`"+`xcode-select --install`+"`"+`
- Linux: `+"`"+`apt install clang`+"`"+`

`+"```"+`sh
jerry run main.jer    # compile and run immediately
jerry compile main.jer -o %s   # compile to a binary
`+"```"+`

## Remote modules

Dependencies are listed in `+"`jerry.remotes`"+`. They are fetched automatically on first build.
To add a new module:

`+"```"+`sh
jerry get github.com/owner/repo@v1.0.0
`+"```"+`
`, name, installSection, name)
}

func createWorkflow(name, tapOwner string) string {
	tapStep := ""
	if tapOwner != "" {
		tapStep = fmt.Sprintf(`
      - name: Update Homebrew tap formula
        if: ${{ !contains(github.ref_name, '-') }}
        env:
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
        run: |
          TAG="${{ github.ref_name }}"
          SHA_LINUX=$(sha256sum dist/%s-linux-x86_64.tar.gz   | awk '{print $1}')
          SHA_MACOS_X86=$(sha256sum dist/%s-macos-x86_64.tar.gz | awk '{print $1}')
          SHA_MACOS_ARM=$(sha256sum dist/%s-macos-arm64.tar.gz  | awk '{print $1}')
          git clone "https://github.com/%s/homebrew-%s.git" tap
          sed -i "s|version \".*\"|version \"${TAG#v}\"|"                  tap/Formula/%s.rb
          sed -i "s|/releases/download/v[^/]*/|/releases/download/${TAG}/|g" tap/Formula/%s.rb
          sed -i "s|SHA256_MACOS_ARM64|${SHA_MACOS_ARM}|"                  tap/Formula/%s.rb
          sed -i "s|SHA256_MACOS_X86_64|${SHA_MACOS_X86}|"                 tap/Formula/%s.rb
          sed -i "s|SHA256_LINUX_X86_64|${SHA_LINUX}|"                     tap/Formula/%s.rb
          cd tap
          git config user.name  "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git remote set-url origin "https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/%s/homebrew-%s.git"
          git add Formula/%s.rb
          git commit -m "chore: update %s to ${TAG}" || echo "no changes"
          git push
`,
			name, name, name,
			tapOwner, name,
			name, name,
			name, name, name,
			tapOwner, name, name, name,
		)
	}

	return fmt.Sprintf(`name: Release

on:
  push:
    branches: [main]
    tags:     ["v*.*.*"]
  pull_request:
    branches: [main]

permissions:
  contents: write

jobs:
  build:
    name: Build (${{ matrix.name }})
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        include:
          - name:       linux-x86_64
            os:         ubuntu-latest
            asset_name: %s-linux-x86_64

          - name:       macos-x86_64
            os:         macos-latest
            asset_name: %s-macos-x86_64
            jerry_target: x86_64-apple-darwin

          - name:       macos-arm64
            os:         macos-latest
            asset_name: %s-macos-arm64
            jerry_target: ""

    steps:
      - uses: actions/checkout@v4

      - name: Install clang (Linux)
        if: runner.os == 'Linux'
        run: |
          sudo apt-get update -qq
          sudo apt-get install -y clang

      - uses: jeffscottbrown/jerry-lang/.github/actions/setup-jerry@main

      - name: Build
        run: |
          TARGET_FLAG=""
          if [ -n "${{ matrix.jerry_target }}" ]; then
            TARGET_FLAG="--target ${{ matrix.jerry_target }}"
          fi
          jerry compile main.jer -o ${{ matrix.asset_name }} $TARGET_FLAG

      - name: Run
        if: matrix.jerry_target == ''
        run: jerry run main.jer

      - name: Archive
        if: startsWith(github.ref, 'refs/tags/v')
        run: tar -czf ${{ matrix.asset_name }}.tar.gz ${{ matrix.asset_name }}

      - name: Upload artifact
        if: startsWith(github.ref, 'refs/tags/v')
        uses: actions/upload-artifact@v4
        with:
          name: ${{ matrix.asset_name }}
          path: ${{ matrix.asset_name }}.tar.gz
          retention-days: 1

  release:
    name: Create GitHub Release
    if: startsWith(github.ref, 'refs/tags/v')
    needs: build
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          path: dist/
          merge-multiple: true

      - name: Generate checksums
        working-directory: dist
        run: sha256sum *.tar.gz > checksums.txt

      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          name: "${{ github.ref_name }}"
          draft: false
          prerelease: ${{ contains(github.ref_name, '-') }}
          generate_release_notes: true
          files: |
            dist/*
%s`, name, name, name, tapStep)
}

func createFormula(name, owner string) string {
	return fmt.Sprintf(`class %s < Formula
  desc "%s"
  homepage "https://github.com/%s/%s"
  version "0.0.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/%s/%s/releases/download/v0.0.0/%s-macos-arm64.tar.gz"
      sha256 "SHA256_MACOS_ARM64"
    end
    on_intel do
      url "https://github.com/%s/%s/releases/download/v0.0.0/%s-macos-x86_64.tar.gz"
      sha256 "SHA256_MACOS_X86_64"
    end
  end
  on_linux do
    on_intel do
      url "https://github.com/%s/%s/releases/download/v0.0.0/%s-linux-x86_64.tar.gz"
      sha256 "SHA256_LINUX_X86_64"
    end
  end

  def install
    bin.install Dir["%s-*"].first => "%s"
  end

  test do
    assert_shell_output "#{bin}/%s"
  end
end
`,
		toRubyClass(name), name,
		owner, name,
		owner, name, name,
		owner, name, name,
		owner, name, name,
		name, name, name,
	)
}

func createTapReadme(name, owner string) string {
	return fmt.Sprintf(`# homebrew-%s

Homebrew tap for [%s](https://github.com/%s/%s).

## Install

`+"```"+`sh
brew tap %s/%s
brew install %s
`+"```"+`

The formula is updated automatically when a new release is published.
`, name, name, owner, name, owner, name, name)
}

func toRubyClass(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	var sb strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return sb.String()
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
