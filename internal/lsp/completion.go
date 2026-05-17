package lsp

import (
	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/parser"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Static completion items built once at startup.
var staticItems []protocol.CompletionItem

func init() {
	kwKind := protocol.CompletionItemKindKeyword
	typeKind := protocol.CompletionItemKindClass
	fnKind := protocol.CompletionItemKindFunction
	constKind := protocol.CompletionItemKindConstant

	keywords := []string{
		"let", "fn", "class", "extends", "if", "else",
		"while", "for", "return", "new", "this", "null",
		"break", "continue", "include",
	}
	types := []string{"int", "float", "bool", "string", "void"}
	boolLits := []string{"true", "false"}
	builtins := []string{
		// compiler primitives
		"print", "println", "write", "len", "push",
		"exit", "panic", "args", "read_stdin", "print_err",
		"read_file", "write_file", "each_line",
		"char_at", "string_slice", "char_to_string",
		"int_to_string", "float_to_string",
		"now_millis", "now_seconds", "now_string",
		// core stdlib (always auto-included)
		"int_abs", "int_max", "int_min",
		"float_abs", "float_max", "float_min",
		"bool_to_string",
	}

	for _, kw := range keywords {
		k := kw
		staticItems = append(staticItems, protocol.CompletionItem{
			Label: k,
			Kind:  &kwKind,
		})
	}
	for _, t := range types {
		t := t
		staticItems = append(staticItems, protocol.CompletionItem{
			Label: t,
			Kind:  &typeKind,
		})
	}
	for _, b := range boolLits {
		b := b
		staticItems = append(staticItems, protocol.CompletionItem{
			Label: b,
			Kind:  &constKind,
		})
	}
	for _, fn := range builtins {
		fn := fn
		staticItems = append(staticItems, protocol.CompletionItem{
			Label:            fn,
			Kind:             &fnKind,
			InsertText:       strPtr(fn + "($0)"),
			InsertTextFormat: insertTextFormatSnippet(),
		})
	}
}

// completionsForSource returns the static items plus any user-defined function
// and class names found by parsing src.
func completionsForSource(src string) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, len(staticItems))
	copy(items, staticItems)

	prog, err := parser.Parse("", src)
	if err != nil {
		return items
	}
	items = append(items, userDefinedItems(prog)...)
	return items
}

func userDefinedItems(prog *ast.Program) []protocol.CompletionItem {
	fnKind := protocol.CompletionItemKindFunction
	classKind := protocol.CompletionItemKindClass

	var items []protocol.CompletionItem
	for _, tl := range prog.Stmts {
		switch {
		case tl.FnDecl != nil:
			name := tl.FnDecl.Name
			items = append(items, protocol.CompletionItem{
				Label:            name,
				Kind:             &fnKind,
				InsertText:       strPtr(name + "($0)"),
				InsertTextFormat: insertTextFormatSnippet(),
			})
		case tl.Class != nil:
			name := tl.Class.Name
			items = append(items, protocol.CompletionItem{
				Label: name,
				Kind:  &classKind,
			})
		}
	}
	return items
}

func insertTextFormatSnippet() *protocol.InsertTextFormat {
	f := protocol.InsertTextFormatSnippet
	return &f
}
