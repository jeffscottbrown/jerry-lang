// Package lsp implements a Jerry Language Server Protocol server.
//
// It is started by `jerry lsp` and communicates with the editor over stdio
// using JSON-RPC 2.0 (the standard LSP transport).
//
// Phase 1 — diagnostics: parse errors and type errors are published after every
// textDocument/didOpen and textDocument/didChange notification.
//
// Phase 2 — completions: a static list of keywords, primitive types, and
// built-in/stdlib functions, augmented with user-defined function and class
// names extracted from the current file.
package lsp

import (
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

const lsName = "jerry-lsp"

var lsVersion = "0.1.0"

// lsHandler is stored at package level so initialize() can call
// CreateServerCapabilities() without needing ctx.Handler (which doesn't exist).
var lsHandler protocol.Handler

// Run starts the LSP server on stdio and blocks until the client disconnects.
func Run() error {
	lsHandler = protocol.Handler{
		Initialize:              initialize,
		Initialized:             initialized,
		Shutdown:                shutdown,
		TextDocumentDidOpen:     didOpen,
		TextDocumentDidChange:   didChange,
		TextDocumentDidClose:    didClose,
		TextDocumentCompletion:  completion,
		TextDocumentCodeLens:    codeLens,
		WorkspaceExecuteCommand: executeCommand,
	}
	srv := server.NewServer(&lsHandler, lsName, false)
	return srv.RunStdio()
}

// ── LSP lifecycle ────────────────────────────────────────────────────────────

func initialize(_ *glsp.Context, _ *protocol.InitializeParams) (any, error) {
	capabilities := lsHandler.CreateServerCapabilities()

	// Full-document sync: the client always sends the complete file text.
	syncKind := protocol.TextDocumentSyncKindFull
	capabilities.TextDocumentSync = syncKind

	// Advertise snippet support in completions.
	capabilities.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{"."},
	}

	// Advertise code lens support so editors send textDocument/codeLens requests.
	capabilities.CodeLensProvider = &protocol.CodeLensOptions{}

	// Advertise executeCommand support for jerry.run.
	capabilities.ExecuteCommandProvider = &protocol.ExecuteCommandOptions{
		Commands: []string{runCommandID},
	}

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    lsName,
			Version: &lsVersion,
		},
	}, nil
}

func initialized(_ *glsp.Context, _ *protocol.InitializedParams) error { return nil }
func shutdown(_ *glsp.Context) error                                    { return nil }

// ── Document lifecycle ───────────────────────────────────────────────────────

func didOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	text := params.TextDocument.Text
	storeDoc(uri, text)
	diagnoseAndPublish(ctx, uri, text)
	return nil
}

func didChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	for _, raw := range params.ContentChanges {
		// Full sync → each change carries the complete document text.
		if whole, ok := raw.(protocol.TextDocumentContentChangeEventWhole); ok {
			storeDoc(uri, whole.Text)
			diagnoseAndPublish(ctx, uri, whole.Text)
			break
		}
	}
	return nil
}

func didClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	deleteDoc(uri)
	// Clear diagnostics so stale squiggles don't persist after the file closes.
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         protocol.DocumentUri(uri),
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

// ── Completions ──────────────────────────────────────────────────────────────

func completion(ctx *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := string(params.TextDocument.URI)
	src, ok := loadDoc(uri)
	if !ok {
		return staticItems, nil
	}
	return completionsForSource(src), nil
}
