package lsp

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// diagnoseAndPublish runs jerry-compiler --check on src and sends
// publishDiagnostics to the client via ctx.Notify.
func diagnoseAndPublish(ctx *glsp.Context, uri, src string) {
	diags := diagnose(uri, src)
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         protocol.DocumentUri(uri),
		Diagnostics: diags,
	})
}

// diagnose writes src to a temp file, runs jerry-compiler --check on it, and
// converts the output into LSP diagnostics.
func diagnose(uri, src string) []protocol.Diagnostic {
	compiler, err := findJerryCompiler()
	if err != nil {
		return nil // no jerry-compiler available; diagnostics silently disabled
	}

	tmp, err := os.CreateTemp("", "jerry-lsp-*.jer")
	if err != nil {
		return nil
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(src); err != nil {
		tmp.Close()
		return nil
	}
	tmp.Close()

	// --check: parse + type-check only; errors go to stdout (exit 1 on error).
	out, _ := exec.Command(compiler, "--check", tmp.Name()).Output()
	return parseCheckOutput(string(out))
}

// errLineRe matches both:
//
//	parse error at LINE:COL: MESSAGE
//	LINE:COL: MESSAGE
var errLineRe = regexp.MustCompile(`^(?:parse error at )?(\d+):(\d+):\s*(.+)$`)

// parseCheckOutput converts jerry-compiler --check stdout into LSP diagnostics.
func parseCheckOutput(out string) []protocol.Diagnostic {
	var diags []protocol.Diagnostic
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		m := errLineRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		msg := m[3]
		sev := protocol.DiagnosticSeverityError
		diags = append(diags, protocol.Diagnostic{
			Range:    lspRangeAt(line, col),
			Severity: &sev,
			Message:  msg,
			Source:   strPtr("jerry"),
		})
	}
	return diags
}

// lspRangeAt converts a 1-based line/col to a single-character LSP Range.
func lspRangeAt(line, col int) protocol.Range {
	l, c := uint32(0), uint32(0)
	if line > 0 {
		l = uint32(line - 1)
	}
	if col > 0 {
		c = uint32(col - 1)
	}
	pos := protocol.Position{Line: l, Character: c}
	end := protocol.Position{Line: l, Character: c + 1}
	return protocol.Range{Start: pos, End: end}
}

// findJerryCompiler returns the path to the jerry-compiler binary.
// Search order: JERRY_COMPILER env var → beside this binary → PATH.
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
	return "", os.ErrNotExist
}

// filenameFromURI strips the "file://" prefix.
func filenameFromURI(uri string) string {
	const prefix = "file://"
	if strings.HasPrefix(uri, prefix) {
		return uri[len(prefix):]
	}
	return uri
}

func strPtr(s string) *string { return &s }
