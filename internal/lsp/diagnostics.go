package lsp

import (
	"errors"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
	"github.com/jeffscottbrown/jerry-lang/internal/parser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// diagnoseAndPublish runs parse + check on src and sends publishDiagnostics to
// the client via ctx.Notify.
func diagnoseAndPublish(ctx *glsp.Context, uri, src string) {
	diags := diagnose(uri, src)
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         protocol.DocumentUri(uri),
		Diagnostics: diags,
	})
}

// diagnose parses and type-checks src, returning LSP diagnostics.
// We run a single-file check (no cross-file or stdlib resolution) which catches
// syntax errors, undefined variables, and type mismatches within the file.
func diagnose(uri, src string) []protocol.Diagnostic {
	prog, parseErr := parser.Parse(filenameFromURI(uri), src)
	if parseErr != nil {
		return []protocol.Diagnostic{parseErrToDiagnostic(parseErr)}
	}

	_, checkErrs := checker.Check(prog)
	diags := make([]protocol.Diagnostic, 0, len(checkErrs))
	for _, ce := range checkErrs {
		diags = append(diags, checkErrToDiagnostic(ce))
	}
	return diags
}

func parseErrToDiagnostic(err error) protocol.Diagnostic {
	var pos lexer.Position
	var msg string

	var pe participle.Error
	if errors.As(err, &pe) {
		pos = pe.Position()
		msg = pe.Message()
	} else {
		msg = err.Error()
	}

	sev := protocol.DiagnosticSeverityError
	return protocol.Diagnostic{
		Range:    lspRange(pos),
		Severity: &sev,
		Message:  msg,
		Source:   strPtr("jerry"),
	}
}

func checkErrToDiagnostic(ce checker.CheckError) protocol.Diagnostic {
	sev := protocol.DiagnosticSeverityError
	return protocol.Diagnostic{
		Range:    lspRange(ce.Pos),
		Severity: &sev,
		Message:  ce.Msg,
		Source:   strPtr("jerry"),
	}
}

// lspRange converts a Participle lexer.Position (1-based) to an LSP Range
// (0-based).  The range spans a single character at the error position.
func lspRange(pos lexer.Position) protocol.Range {
	line := uint32(0)
	col := uint32(0)
	if pos.Line > 0 {
		line = uint32(pos.Line - 1)
	}
	if pos.Column > 0 {
		col = uint32(pos.Column - 1)
	}
	start := protocol.Position{Line: line, Character: col}
	end := protocol.Position{Line: line, Character: col + 1}
	return protocol.Range{Start: start, End: end}
}

// filenameFromURI strips the "file://" prefix for use as a Participle filename.
func filenameFromURI(uri string) string {
	const prefix = "file://"
	if len(uri) > len(prefix) && uri[:len(prefix)] == prefix {
		return uri[len(prefix):]
	}
	return uri
}

func strPtr(s string) *string { return &s }
