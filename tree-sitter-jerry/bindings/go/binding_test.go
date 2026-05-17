package tree_sitter_jerry_test

import (
	"testing"

	tree_sitter "github.com/smacker/go-tree-sitter"
	"github.com/tree-sitter/tree-sitter-jerry"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_jerry.Language())
	if language == nil {
		t.Errorf("Error loading Jerry grammar")
	}
}
