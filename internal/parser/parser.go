// Package parser builds an Jerry AST from source text using Participle.
package parser

import (
	"github.com/jeffscottbrown/jerry-lang/internal/ast"

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

	// ThisKW and NullKW are their own token types (like BoolLit) so that string
	// literals "this" and "null" are never mis-classified as keywords after Unquote
	// strips their surrounding quotes.
	{Name: "ThisKW", Pattern: `\bthis\b`},
	{Name: "NullKW", Pattern: `\bnull\b`},

	// Keywords — matched before Ident, after BoolLit/ThisKW/NullKW
	{Name: "Keyword", Pattern: `\b(?:let|fn|class|extends|if|else|while|for|return|new|break|continue|include)\b`},

	// Identifier
	{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_]*`},

	// Multi-character operators (longest match first), then single-char.
	// Bang and Minus are separate types so UnaryExpr grammar can match them
	// by token type rather than value (avoiding false matches on string literals
	// like "!" or "-" after Unquote strips the surrounding quotes).
	{Name: "Op",    Pattern: `&&|\|\||==|!=|<=|>=|\+\+|--|[+*/%=<>.,;:(){}\[\]@]`},
	{Name: "Bang",  Pattern: `!`},
	{Name: "Minus", Pattern: `-`},
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
