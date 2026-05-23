// Command jerry-get: fetch a remote Jerry module and record it in jerry.remotes.
// Usage: jerry-get <module>@<version>
// Invoked by `jerry get`.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/modfile"
	"github.com/jeffscottbrown/jerry-lang/internal/module"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: jerry-get <module>@<version>")
		os.Exit(1)
	}
	if err := cmdGet(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

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

func splitAtVersion(arg string) (modPath, version string, ok bool) {
	idx := strings.LastIndex(arg, "@")
	if idx < 0 {
		return "", "", false
	}
	return arg[:idx], arg[idx+1:], true
}
