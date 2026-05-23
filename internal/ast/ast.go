// Package ast defines the Jerry abstract syntax tree.
// Nodes are produced by the parser and annotated by the type checker.
package ast

import "github.com/alecthomas/participle/v2/lexer"

// ── Top level ────────────────────────────────────────────────────────────────

type Program struct {
	Pos   lexer.Position
	Stmts []*TopLevel `{ @@ }`
}

// TopLevel is anything that can appear at module scope.
type TopLevel struct {
	Pos     lexer.Position
	Include *IncludeDecl `( @@`
	FnDecl  *FnDecl      `| @@`
	Class   *ClassDecl   `| @@`
	VarDecl *VarDecl     `| @@ )`
}

// IncludeDecl brings a module into scope for this file.
//
// Stdlib:  include @string          (resolved from embedded stdlib)
// Remote:  include "github.com/x/y" (resolved via jerry.mod)
type IncludeDecl struct {
	Pos    lexer.Position
	Stdlib string `"include" ( "@" @Ident`
	Remote string `           | @String )`
}

// ── Declarations ─────────────────────────────────────────────────────────────

type FnDecl struct {
	Pos    lexer.Position
	// Allow "new" and "init" as method names even though they are keywords.
	Name   string   `"fn" ( @Ident | @"new" | @NullKW )`
	Params []*Param `"(" [ @@ { "," @@ } ] ")"`
	Ret    *TypeExpr `[ ":" @@ ]`
	Body   *Block   `@@`
}

type Param struct {
	Pos  lexer.Position
	Name string    `@Ident ":"`
	Type *TypeExpr `@@`
}

type ClassDecl struct {
	Pos     lexer.Position
	Name    string         `"class" @Ident`
	Extends string         `[ "extends" @Ident ]`
	Members []*ClassMember `"{" { @@ } "}"`
}

type ClassMember struct {
	Pos    lexer.Position
	Method *FnDecl   `( @@`
	Field  *FieldDecl `| @@ )`
}

type FieldDecl struct {
	Pos  lexer.Position
	Name string    `@Ident ":"`
	Type *TypeExpr `@@ ";"`
}

// ── Statements ───────────────────────────────────────────────────────────────

type Stmt interface{ stmtNode() }

type Block struct {
	Pos   lexer.Position
	Stmts []*StmtNode `"{" { @@ } "}"`
}

// StmtNode is a discriminated union of all statement kinds.
type StmtNode struct {
	Pos       lexer.Position
	VarDecl   *VarDecl   `( @@`
	Return    *ReturnStmt `| @@`
	If        *IfStmt    `| @@`
	While     *WhileStmt `| @@`
	For       *ForStmt   `| @@`
	Break     *BreakStmt `| @@`
	Continue  *ContinueStmt `| @@`
	ExprStmt  *ExprStmt  `| @@ )`
}

type VarDecl struct {
	Pos   lexer.Position
	Name  string    `"let" @Ident`
	Ann   *TypeExpr `[ ":" @@ ]`
	Value *Expr     `"=" @@ ";"`
}

type ReturnStmt struct {
	Pos   lexer.Position
	Value *Expr `"return" [ @@ ] ";"`
}

type IfStmt struct {
	Pos  lexer.Position
	Cond *Expr      `"if" @@`
	Then *Block     `@@`
	Else *ElsePart  `[ @@ ]`
}

type ElsePart struct {
	Pos   lexer.Position
	// "else if ..." or "else { ... }"
	ElseIf *IfStmt `( "else" @@`
	Block  *Block  `| "else" @@ )`
}

type WhileStmt struct {
	Pos  lexer.Position
	Cond *Expr  `"while" @@`
	Body *Block `@@`
}

type ForStmt struct {
	Pos  lexer.Position
	Init *ForInit `"for" "(" [ @@ ] ";"`
	Cond *Expr    `[ @@ ] ";"`
	Post *Expr    `[ @@ ] ")"`
	Body *Block   `@@`
}

type ForInit struct {
	Pos     lexer.Position
	VarDecl *ForVarDecl `( @@`
	Expr    *Expr       `| @@ )`
}

// ForVarDecl is like VarDecl but without the trailing semicolon.
type ForVarDecl struct {
	Pos   lexer.Position
	Name  string    `"let" @Ident`
	Ann   *TypeExpr `[ ":" @@ ]`
	Value *Expr     `"=" @@`
}

type BreakStmt struct {
	Pos     lexer.Position
	Keyword string `@"break" ";"`
}

type ContinueStmt struct {
	Pos     lexer.Position
	Keyword string `@"continue" ";"`
}

type ExprStmt struct {
	Pos  lexer.Position
	Expr *Expr `@@ ";"`
}

// ── Type expressions ─────────────────────────────────────────────────────────

// TypeExpr represents a type annotation.
// Examples: int, string, MyClass, int[], fn(int): bool, map<string, int>
type TypeExpr struct {
	Pos     lexer.Position
	// Map type: map<K, V>
	MapType *MapTypeExpr `( @@`
	// Named type (possibly array): int, string, MyClass, int[]
	Name    string       `| @Ident`
	IsArray []string     `  { "[" @"]" }`
	// Function type: fn(T, T): T
	FnType  *FnTypeExpr  `| @@ )`
}

// MapTypeExpr represents a map type annotation: map<K, V>
type MapTypeExpr struct {
	Pos   lexer.Position
	Key   *TypeExpr `"map" "<" @@`
	Value *TypeExpr `"," @@ ">"`
}

// FnTypeExpr represents a function type: fn(ParamTypes...): ReturnType
type FnTypeExpr struct {
	Pos    lexer.Position
	Params []*TypeExpr `"fn" "(" [ @@ { "," @@ } ] ")"`
	Return *TypeExpr   `[ ":" @@ ]`
}

func (t *TypeExpr) Array() bool { return t != nil && len(t.IsArray) > 0 }
func (t *TypeExpr) String() string {
	if t == nil {
		return "void"
	}
	if t.MapType != nil {
		return "map<" + t.MapType.Key.String() + ", " + t.MapType.Value.String() + ">"
	}
	if t.FnType != nil {
		s := "fn("
		for i, p := range t.FnType.Params {
			if i > 0 {
				s += ", "
			}
			s += p.String()
		}
		s += ")"
		if t.FnType.Return != nil {
			s += ": " + t.FnType.Return.String()
		}
		return s
	}
	s := t.Name
	for range t.IsArray {
		s += "[]"
	}
	return s
}

// ── Expressions ──────────────────────────────────────────────────────────────
// Precedence (low → high):
//   Assign → LogicalOr → LogicalAnd → Equality → Compare → Add → Mul → Unary → Postfix → Primary

type Expr struct {
	Pos        lexer.Position
	Assignment *AssignExpr `@@`

	// Filled by the type checker.
	ResolvedType interface{} // *checker.Type — avoid import cycle; cast at use
}

type AssignExpr struct {
	Pos   lexer.Position
	Left  *OrExpr     `@@`
	Right *AssignExpr `[ "=" @@ ]`
}

type OrExpr struct {
	Pos  lexer.Position
	Left *AndExpr  `@@`
	Rest []OrRest  `{ @@ }`
}
type OrRest struct {
	Op    string   `@"||"`
	Right *AndExpr `@@`
}

type AndExpr struct {
	Pos  lexer.Position
	Left *EqExpr   `@@`
	Rest []AndRest `{ @@ }`
}
type AndRest struct {
	Op    string  `@"&&"`
	Right *EqExpr `@@`
}

type EqExpr struct {
	Pos  lexer.Position
	Left *CmpExpr `@@`
	Rest []EqRest `{ @@ }`
}
type EqRest struct {
	Op    string   `@( "==" | "!=" )`
	Right *CmpExpr `@@`
}

type CmpExpr struct {
	Pos  lexer.Position
	Left *AddExpr  `@@`
	Rest []CmpRest `{ @@ }`
}
type CmpRest struct {
	Op    string   `@( "<=" | ">=" | "<" | ">" )`
	Right *AddExpr `@@`
}

type AddExpr struct {
	Pos  lexer.Position
	Left *MulExpr  `@@`
	Rest []AddRest `{ @@ }`
}
type AddRest struct {
	Op    string   `@( "+" | "-" )`
	Right *MulExpr `@@`
}

type MulExpr struct {
	Pos  lexer.Position
	Left *UnaryExpr `@@`
	Rest []MulRest  `{ @@ }`
}
type MulRest struct {
	Op    string     `@( "*" | "/" | "%" )`
	Right *UnaryExpr `@@`
}

type UnaryExpr struct {
	Pos  lexer.Position
	Op   string        `( @( Bang | Minus )`
	Expr *UnaryExpr    `  @@ )`
	Post *PostfixExpr  `| @@`
}

type PostfixExpr struct {
	Pos  lexer.Position
	Base *PrimaryExpr `@@`
	Ops  []PostfixOp  `{ @@ }`
}

type PostfixOp struct {
	Pos        lexer.Position
	Call       *CallArgs `( @@`
	Index      *Expr     `| "[" @@ "]"`
	Field      string    `| "." @Ident`
	PlusPlus   bool      `| @"++"`
	MinusMinus bool      `| @"--" )`
}

type CallArgs struct {
	Pos  lexer.Position
	Args []*Expr `"(" [ @@ { "," @@ } ] ")"`
}

type PrimaryExpr struct {
	Pos    lexer.Position
	// Ordered: most specific first to avoid ambiguity
	FnExpr  *FnExpr   `( @@`
	NewExpr *NewExpr  `| @@`
	MapLit  *MapLit   `| @@`
	Array   *ArrayLit `| @@`
	Paren   *Expr     `| "(" @@ ")"`
	This    bool      `| @ThisKW`
	Null    bool      `| @NullKW`
	Bool    string    `| @BoolLit`
	Float   string    `| @Float`
	Int     string    `| @Int`
	String  *string   `| @String`
	Ident   string    `| @Ident )`
}

type FnExpr struct {
	Pos    lexer.Position
	Params []*Param  `"fn" "(" [ @@ { "," @@ } ] ")"`
	Ret    *TypeExpr `[ ":" @@ ]`
	Body   *Block    `@@`
}

type NewExpr struct {
	Pos       lexer.Position
	ClassName string  `"new" @Ident`
	Args      []*Expr `"(" [ @@ { "," @@ } ] ")"`
}

type ArrayLit struct {
	Pos   lexer.Position
	Elems []*Expr `"[" [ @@ { "," @@ } ] "]"`
}

// MapLit is a map literal: {} or { key: val, key: val, ... }
// ResolvedType is set by the checker so codegen knows key/value types for empty literals.
type MapLit struct {
	Pos          lexer.Position
	Entries      []*MapEntry `"{" [ @@ { "," @@ } ] "}"`
	ResolvedType interface{} // *checker.Type — avoid import cycle; cast at use
}

type MapEntry struct {
	Pos   lexer.Position
	Key   *Expr `@@`
	Value *Expr `":" @@`
}
