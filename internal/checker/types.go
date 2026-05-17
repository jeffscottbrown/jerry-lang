// Package checker defines the Jerry type system and performs semantic analysis.
package checker

import (
	"fmt"

	"github.com/alecthomas/participle/v2/lexer"
)

// CheckError is a type / semantic error with the source position where it occurred.
type CheckError struct {
	Msg string
	Pos lexer.Position
}

func (e CheckError) Error() string { return e.Msg }

// ── Type definitions ─────────────────────────────────────────────────────────

type TypeKind int

const (
	KindVoid TypeKind = iota
	KindInt
	KindFloat
	KindBool
	KindString
	KindArray
	KindFunc
	KindClass
	KindNull
)

type Type struct {
	Kind     TypeKind
	Elem     *Type   // KindArray element type
	Params   []*Type // KindFunc parameter types
	Return   *Type   // KindFunc return type
	ClassName string  // KindClass name
}

var (
	Void   = &Type{Kind: KindVoid}
	Int    = &Type{Kind: KindInt}
	Float  = &Type{Kind: KindFloat}
	Bool   = &Type{Kind: KindBool}
	String = &Type{Kind: KindString}
	Null   = &Type{Kind: KindNull}
)

func ArrayOf(elem *Type) *Type    { return &Type{Kind: KindArray, Elem: elem} }
func ClassType(name string) *Type { return &Type{Kind: KindClass, ClassName: name} }
func FuncType(params []*Type, ret *Type) *Type {
	return &Type{Kind: KindFunc, Params: params, Return: ret}
}

func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.Kind {
	case KindVoid:
		return "void"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindBool:
		return "bool"
	case KindString:
		return "string"
	case KindArray:
		return t.Elem.String() + "[]"
	case KindFunc:
		s := "fn("
		for i, p := range t.Params {
			if i > 0 {
				s += ", "
			}
			s += p.String()
		}
		s += ")"
		if t.Return != nil && t.Return.Kind != KindVoid {
			s += ": " + t.Return.String()
		}
		return s
	case KindClass:
		return t.ClassName
	case KindNull:
		return "null"
	}
	return "unknown"
}

func (t *Type) Equal(other *Type) bool {
	if t == other {
		return true
	}
	if t == nil || other == nil {
		return false
	}
	if t.Kind != other.Kind {
		// null is assignable to class and string types
		if other.Kind == KindNull && (t.Kind == KindClass || t.Kind == KindString) {
			return true
		}
		if t.Kind == KindNull && (other.Kind == KindClass || other.Kind == KindString) {
			return true
		}
		return false
	}
	switch t.Kind {
	case KindArray:
		return t.Elem.Equal(other.Elem)
	case KindFunc:
		if len(t.Params) != len(other.Params) {
			return false
		}
		for i := range t.Params {
			if !t.Params[i].Equal(other.Params[i]) {
				return false
			}
		}
		return t.Return.Equal(other.Return)
	case KindClass:
		return t.ClassName == other.ClassName
	default:
		return true
	}
}

// ── Symbol table ─────────────────────────────────────────────────────────────

type SymbolKind int

const (
	SymVar SymbolKind = iota
	SymFunc
	SymClass
	SymParam
)

type Symbol struct {
	Name string
	Kind SymbolKind
	Type *Type
}

type Scope struct {
	parent  *Scope
	symbols map[string]*Symbol
}

func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent, symbols: make(map[string]*Symbol)}
}

func (s *Scope) Define(sym *Symbol) error {
	if _, exists := s.symbols[sym.Name]; exists {
		return fmt.Errorf("symbol %q already defined in this scope", sym.Name)
	}
	s.symbols[sym.Name] = sym
	return nil
}

func (s *Scope) Lookup(name string) (*Symbol, bool) {
	if sym, ok := s.symbols[name]; ok {
		return sym, true
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return nil, false
}

// ── Type info (checker output) ───────────────────────────────────────────────

// Info holds all type information computed during checking.
// Expressions are stored by pointer identity.
type Info struct {
	// ExprType maps expression pointer (as uintptr) to resolved type.
	ExprType map[uintptr]*Type
	// Classes maps class name → class info.
	Classes map[string]*ClassInfo
	// Funcs maps function name → resolved function type.
	Funcs map[string]*Type
}

func NewInfo() *Info {
	return &Info{
		ExprType: make(map[uintptr]*Type),
		Classes:  make(map[string]*ClassInfo),
		Funcs:    make(map[string]*Type),
	}
}

type ClassInfo struct {
	Name    string
	Fields  map[string]*Type   // field name → type
	Methods map[string]*Type   // method name → func type
	// Ordered lists for codegen layout
	FieldOrder  []string
	MethodOrder []string
}

func NewClassInfo(name string) *ClassInfo {
	return &ClassInfo{
		Name:    name,
		Fields:  make(map[string]*Type),
		Methods: make(map[string]*Type),
	}
}
