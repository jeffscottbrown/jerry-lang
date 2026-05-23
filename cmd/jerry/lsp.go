package main

import (
	"fmt"
	"os"

	"github.com/jeffscottbrown/jerry-lang/internal/lsp"
)

func runLsp() {
	if err := lsp.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "jerry lsp: %v\n", err)
		os.Exit(1)
	}
}
