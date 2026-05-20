package lsp

import (
	"errors"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/build"
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
// It resolves include @stdlib statements so symbols from included modules
// (e.g. assert_eq_int from @testing) are recognised during type-checking.
func diagnose(uri, src string) []protocol.Diagnostic {
	prog, parseErr := parser.Parse(filenameFromURI(uri), src)
	if parseErr != nil {
		return []protocol.Diagnostic{parseErrToDiagnostic(parseErr)}
	}

	fsys := build.StdlibFS()

	// core.jer is always in scope.
	coreAST, _ := build.ParseStdlibFile(fsys, "core")

	// Load any explicitly included stdlib modules.
	stdlibASTs := make(map[string]*ast.Program)
	for _, tl := range prog.Stmts {
		if tl.Include == nil || tl.Include.Stdlib == "" {
			continue
		}
		name := tl.Include.Stdlib
		if _, already := stdlibASTs[name]; already {
			continue
		}
		if stdAST, err := build.ParseStdlibFile(fsys, name); err == nil {
			stdlibASTs[name] = stdAST
		}
	}

	_, checkErrs := checker.CheckAll([]*ast.Program{prog}, coreAST, stdlibASTs, nil)
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
