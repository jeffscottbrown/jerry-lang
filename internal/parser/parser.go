// Package parser builds an Jerry AST from source text using Participle.
package parser

import (
	"jerry/internal/ast"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// Lexer token rules — order matters: longer/more-specific patterns first.
var jerryLexer = lexer.MustSimple([]lexer.SimpleRule{
	{Name: "Whitespace", Pattern: `[ \t\r\n]+`},
	{Name: "LineComment", Pattern: `//[^\n]*`},
	{Name: "BlockComment", Pattern: `/\*[^*]*\*+(?:[^/*][^*]*\*+)*/`},

	// Literals (before Ident so "true" doesn't become Ident)
	{Name: "Float", Pattern: `[0-9]+\.[0-9]+`},
	{Name: "Int", Pattern: `[0-9]+`},
	{Name: "String", Pattern: `"(?:[^"\\]|\\.)*"`},

	// Bool literals matched as their own token type so that string literals
	// containing "true" or "false" are never mis-classified as Bool.
	{Name: "BoolLit", Pattern: `\b(?:true|false)\b`},

	// Keywords — matched before Ident, after BoolLit so true/false stay separate
	{Name: "Keyword", Pattern: `\b(?:let|fn|class|extends|if|else|while|for|return|new|this|null|break|continue|include)\b`},

	// Identifier
	{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_]*`},

	// Multi-character operators (longest match first)
	{Name: "Op", Pattern: `&&|\|\||==|!=|<=|>=|\+\+|--|[+\-*/%=!<>.,;:(){}\[\]@]`},
})

var parser = participle.MustBuild[ast.Program](
	participle.Lexer(jerryLexer),
	participle.Elide("Whitespace", "LineComment", "BlockComment"),
	participle.UseLookahead(participle.MaxLookahead),
	participle.Unquote("String"),
)

// Parse parses Jerry source code and returns the AST.
func Parse(filename, src string) (*ast.Program, error) {
	return parser.ParseString(filename, src)
}
