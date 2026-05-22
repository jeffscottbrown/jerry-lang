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
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/modfile"
	"github.com/jeffscottbrown/jerry-lang/internal/module"
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
		remotes, err := scanRemoteIncludes(src)
		if err != nil {
			continue // ignore errors during sweep
		}
		for _, r := range remotes {
			referenced[r] = true
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

// splitAtVersion splits "github.com/x/y@v1.0.0" into ("github.com/x/y", "v1.0.0", true).
func splitAtVersion(arg string) (modPath, version string, ok bool) {
	idx := strings.LastIndex(arg, "@")
	if idx < 0 {
		return "", "", false
	}
	return arg[:idx], arg[idx+1:], true
}


// ── jerry test ───────────────────────────────────────────────────────────────

// cmdTest discovers *_test.jer files, finds all fn test_*() functions in them,
// generates a temporary main file that calls each one, compiles everything
// together, and runs the resulting binary.
//
// When a directory is given, all .jer files in that directory are included in
// the build so that test files can reference the source under test. Source
// files that define fn main() are excluded to avoid conflicting with the
// generated test main.
func cmdTest(args []string) error {
	// Collect files: explicit .jer args, directory args, or cwd fallback.
	// Directories contribute both source files and test files.
	var testFiles []string   // *_test.jer
	var sourceFiles []string // other .jer files (code under test)

	scanDir := func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("jerry test: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jer") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if strings.HasSuffix(e.Name(), "_test.jer") {
				testFiles = append(testFiles, p)
			} else {
				sourceFiles = append(sourceFiles, p)
			}
		}
		return nil
	}

	for _, a := range args {
		if strings.HasSuffix(a, ".jer") {
			testFiles = append(testFiles, a)
		} else {
			if err := scanDir(a); err != nil {
				return err
			}
		}
	}
	if len(testFiles) == 0 && len(sourceFiles) == 0 {
		if err := scanDir("."); err != nil {
			return err
		}
	}
	if len(testFiles) == 0 {
		fmt.Fprintln(os.Stderr, "jerry test: no test files found")
		return nil
	}

	// Scan each test file for fn test_*() functions using simple text parsing.
	type fileTests struct {
		path  string
		names []string
	}
	var allTests []fileTests
	for _, path := range testFiles {
		names, err := scanTestFunctions(path)
		if err != nil {
			return err
		}
		if len(names) > 0 {
			allTests = append(allTests, fileTests{path, names})
		}
	}
	if len(allTests) == 0 {
		fmt.Fprintln(os.Stderr, "jerry test: no test_* functions found")
		return nil
	}

	// Exclude source files that define fn main() — the generated test main
	// provides main(), so including another would cause a duplicate error.
	var filteredSources []string
	for _, path := range sourceFiles {
		hasMain, err := fileDefinesMain(path)
		if err != nil {
			return err
		}
		if !hasMain {
			filteredSources = append(filteredSources, path)
		}
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

	// Compile: source files (sans main) + test files + generated main.
	srcs := append(filteredSources, testFiles...)
	srcs = append(srcs, tmp.Name())
	tmpDir, err := os.MkdirTemp("", "jerry-test-bin-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	bin := filepath.Join(tmpDir, "test")

	runJerryCompiler(append(srcs, "-o", bin))

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

// scanTestFunctions scans a .jer file for top-level fn test_*() declarations
// using simple line-by-line text parsing. Returns function names found.
func scanTestFunctions(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()
	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Match: fn test_NAME() or fn test_NAME(): void
		if !strings.HasPrefix(line, "fn test_") {
			continue
		}
		rest := line[3:] // strip "fn "
		paren := strings.Index(rest, "(")
		if paren < 0 {
			continue
		}
		name := rest[:paren]
		// Ensure no parameters (empty parens or just whitespace)
		close := strings.Index(rest[paren:], ")")
		if close < 0 {
			continue
		}
		if strings.TrimSpace(rest[paren+1:paren+close]) == "" {
			names = append(names, name)
		}
	}
	return names, scanner.Err()
}

// fileDefinesMain reports whether a .jer file contains a top-level fn main declaration.
func fileDefinesMain(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "fn main(") || strings.HasPrefix(line, "fn main():") {
			return true, nil
		}
	}
	return false, scanner.Err()
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
	return fmt.Sprintf(`include @string

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
	return ""
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

// ── jerry-compiler discovery and invocation ───────────────────────────────────

// findJerryCompiler returns the path to the jerry-compiler binary.
// Search order:
//  1. JERRY_COMPILER env var
//  2. <directory of this binary>/jerry-compiler
//  3. jerry-compiler on PATH
func findJerryCompiler() (string, error) {
	if p := os.Getenv("JERRY_COMPILER"); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "jerry-compiler")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("jerry-compiler"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("jerry-compiler not found; run `make install` or set JERRY_COMPILER")
}

// runJerryCompiler invokes jerry-compiler with args, inheriting stdio.
// Exits the process if jerry-compiler is not found or exits non-zero.
func runJerryCompiler(args []string) {
	bin, err := findJerryCompiler()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jerry: "+err.Error())
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

// scanRemoteIncludes scans a .jer file for `include "..."` remote declarations.
func scanRemoteIncludes(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var remotes []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, `include "`) {
			continue
		}
		rest := line[len(`include "`):]
		end := strings.Index(rest, `"`)
		if end > 0 {
			remotes = append(remotes, rest[:end])
		}
	}
	return remotes, scanner.Err()
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
