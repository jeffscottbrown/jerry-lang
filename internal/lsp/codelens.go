package lsp

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

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

	line, col, found := findMainPos(src)
	if !found {
		return nil, nil
	}

	return []protocol.CodeLens{
		{
			Range: lspRangeAt(line, col),
			Command: &protocol.Command{
				Title:     "▶ Run",
				Command:   runCommandID,
				Arguments: []any{uri},
			},
		},
	}, nil
}

// findMainPos scans src for a top-level `fn main()` with no parameters.
// Returns 1-based line and column of the `fn` keyword, and whether it was found.
func findMainPos(src string) (line, col int, found bool) {
	scanner := bufio.NewScanner(strings.NewReader(src))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmed, "fn main(") {
			continue
		}
		// Verify no parameters between the parens.
		rest := trimmed[len("fn main("):]
		close := strings.Index(rest, ")")
		if close < 0 || strings.TrimSpace(rest[:close]) != "" {
			continue
		}
		col := strings.Index(text, "fn main") + 1
		return lineNum, col, true
	}
	return 0, 0, false
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
