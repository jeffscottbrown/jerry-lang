// Command jerry-lsp: Jerry Language Server Protocol server.
// Invoked by `jerry lsp` or directly by editors via the setup-jerry action.
package main

import (
	"fmt"
	"os"

	"github.com/jeffscottbrown/jerry-lang/internal/lsp"
)

func main() {
	if err := lsp.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "jerry lsp: %v\n", err)
		os.Exit(1)
	}
}
