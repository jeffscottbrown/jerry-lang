package lsp

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/parser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const runCommandID = "jerry.run"

// codeLens returns a "▶ Run" lens at the position of `fn main()` if present.
func codeLens(_ *glsp.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	uri := string(params.TextDocument.URI)
	src, ok := loadDoc(uri)
	if !ok {
		return nil, nil
	}

	prog, err := parser.Parse(filenameFromURI(uri), src)
	if err != nil {
		return nil, nil
	}

	for _, tl := range prog.Stmts {
		if tl.FnDecl != nil && tl.FnDecl.Name == "main" && len(tl.FnDecl.Params) == 0 {
			rng := lspRange(tl.FnDecl.Pos)
			return []protocol.CodeLens{
				{
					Range: rng,
					Command: &protocol.Command{
						Title:     "▶ Run",
						Command:   runCommandID,
						Arguments: []any{uri},
					},
				},
			}, nil
		}
	}
	return nil, nil
}

// executeCommand handles workspace/executeCommand for the jerry.run command.
// It runs `jerry run <file>` and reports the output via window/showMessage.
func executeCommand(ctx *glsp.Context, params *protocol.ExecuteCommandParams) (any, error) {
	if params.Command != runCommandID || len(params.Arguments) == 0 {
		return nil, nil
	}

	uri, ok := params.Arguments[0].(string)
	if !ok {
		return nil, nil
	}
	filePath := filenameFromURI(uri)

	cmd := exec.Command("jerry", "run", filePath)
	out, runErr := cmd.CombinedOutput()

	msgType := protocol.MessageTypeInfo
	msg := strings.TrimSpace(string(out))
	if runErr != nil {
		msgType = protocol.MessageTypeError
		if msg == "" {
			msg = runErr.Error()
		}
	}
	if msg == "" {
		msg = "(no output)"
	}

	ctx.Notify(protocol.ServerWindowShowMessage, &protocol.ShowMessageParams{
		Type:    msgType,
		Message: fmt.Sprintf("jerry run:\n%s", msg),
	})
	return nil, nil
}
