// Package modfile parses and writes jerry.remotes and jerry.sum files.
package modfile

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

const RemotesFileName = "jerry.remotes"
const SumFileName = "jerry.sum"

// ModFile represents the contents of a jerry.remotes file.
type ModFile struct {
	Requires map[string]string // import path → version
}

// Parse reads and parses a jerry.remotes file.
// If the file does not exist, an empty ModFile is returned without error.
func Parse(path string) (*ModFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ModFile{Requires: make(map[string]string)}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseBytes(data)
}

func parseBytes(data []byte) (*ModFile, error) {
	mf := &ModFile{Requires: make(map[string]string)}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			mf.Requires[parts[0]] = parts[1]
		}
	}
	return mf, scanner.Err()
}

// Write serialises mf to path.
func Write(path string, mf *ModFile) error {
	var sb strings.Builder
	for _, k := range sortedKeys(mf.Requires) {
		fmt.Fprintf(&sb, "%s %s\n", k, mf.Requires[k])
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// ── SumFile ───────────────────────────────────────────────────────────────────

// SumFile maps "modpath@version" → "h1:<hex-sha256>".
type SumFile map[string]string

// ParseSum reads jerry.sum. Missing file is not an error; returns empty map.
func ParseSum(path string) (SumFile, error) {
	sums := make(SumFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return sums, nil
	}
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 3 {
			sums[parts[0]+"@"+parts[1]] = parts[2]
		}
	}
	return sums, scanner.Err()
}

// Write serialises the sum file to path.
func (s SumFile) Write(path string) error {
	var sb strings.Builder
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		at := strings.LastIndex(k, "@")
		mod, ver := k[:at], k[at+1:]
		fmt.Fprintf(&sb, "%s %s %s\n", mod, ver, s[k])
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// Key returns the canonical key for a module/version pair.
func (s SumFile) Key(modPath, version string) string {
	return modPath + "@" + version
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
