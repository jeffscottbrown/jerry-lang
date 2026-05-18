// Package build implements the Jerry compilation pipeline:
// parse → stdlib/remote resolution → type-check → codegen → clang.
package build

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// Compile compiles srcs to a native binary at outBin.
// If target is non-empty it is passed to clang as --target.
func Compile(srcs []string, outBin, target string) error {
	ir, err := CompileToIR(srcs)
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

	runtimeLib, cleanupRuntime, err := ExtractRuntime()
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

// CompileToIR is the main compilation pipeline:
//  1. Parse stdlib/core.jer (always included).
//  2. Parse each project file independently.
//  3. Resolve include @stdlib and include "remote" declarations.
//  4. Type-check all files with per-file scoping via checker.CheckAll.
//  5. Generate LLVM IR from all ASTs together.
func CompileToIR(srcs []string) (string, error) {
	fsys := StdlibFS()

	// Always load core.jer.
	coreAST, err := ParseStdlibFile(fsys, "core")
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
			stdAST, err := ParseStdlibFile(fsys, name)
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
	for _, name := range sortedKeys(stdlibASTs) {
		allASTs = append(allASTs, stdlibASTs[name])
	}
	for _, path := range sortedKeys(remoteASTs) {
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

	// Resolve jerry.remotes and jerry.sum from the working directory.
	projectDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	mf, err := modfile.Parse(filepath.Join(projectDir, modfile.RemotesFileName))
	if err != nil {
		return nil, fmt.Errorf("reading jerry.remotes: %w", err)
	}

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

		cacheDir, actualHash, err := module.Fetch(modPath, version)
		if err != nil {
			return nil, fmt.Errorf("fetching module %s@%s: %w", modPath, version, err)
		}

		key := sums.Key(modPath, version)
		if expectedHash, has := sums[key]; has && expectedHash != actualHash {
			return nil, fmt.Errorf("hash mismatch for %s@%s: expected %s, got %s",
				modPath, version, expectedHash, actualHash)
		}

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

// ParseStdlibFile reads and parses a stdlib module by name (without .jer extension).
func ParseStdlibFile(fsys fs.FS, name string) (*ast.Program, error) {
	filename := name + ".jer"
	data, err := fs.ReadFile(fsys, filename)
	if err != nil {
		return nil, err
	}
	return parser.Parse("stdlib/"+filename, string(data))
}

// StdlibFS returns the filesystem for stdlib lookups.
// If the JERRY_STDLIB environment variable points to a directory, an OS-based
// FS rooted there is returned — useful when developing the stdlib itself.
// Otherwise the embedded FS baked into the binary is used.
func StdlibFS() fs.FS {
	if dir := os.Getenv("JERRY_STDLIB"); dir != "" {
		return os.DirFS(dir)
	}
	return stdlib.Files
}

// ExtractRuntime writes the embedded runtime C sources to a temp directory
// and returns the path to runtime.c. The caller must call cleanup when done.
func ExtractRuntime() (runtimeC string, cleanup func(), err error) {
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
