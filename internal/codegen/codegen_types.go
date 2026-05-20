package codegen

import (
	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
)

// ── LLVM type mapping ─────────────────────────────────────────────────────────

func (g *Generator) llvmType(t *checker.Type) string {
	if t == nil {
		return "void"
	}
	switch t.Kind {
	case checker.KindVoid:
		return "void"
	case checker.KindInt:
		return "i64"
	case checker.KindFloat:
		return "double"
	case checker.KindBool:
		return "i1"
	case checker.KindString:
		return "ptr" // JerryStr*
	case checker.KindArray:
		return "ptr" // JerryArray*
	case checker.KindClass:
		return "ptr" // ClassName*
	case checker.KindFunc:
		return "ptr" // JerryClosure*
	case checker.KindNull:
		return "ptr"
	}
	return "ptr"
}

func (g *Generator) typeSize(t *checker.Type) int64 {
	switch t.Kind {
	case checker.KindInt:
		return 8
	case checker.KindFloat:
		return 8
	case checker.KindBool:
		return 1
	default:
		return 8 // pointer size on x86-64
	}
}

func (g *Generator) zeroValue(t *checker.Type) string {
	if t == nil {
		return "0"
	}
	switch t.Kind {
	case checker.KindInt:
		return "0"
	case checker.KindFloat:
		return "0.0"
	case checker.KindBool:
		return "false"
	default:
		return "null"
	}
}

func (g *Generator) resolveTypeExpr(te *ast.TypeExpr) *checker.Type {
	if te == nil {
		return checker.Void
	}
	if te.FnType != nil {
		var params []*checker.Type
		for _, p := range te.FnType.Params {
			params = append(params, g.resolveTypeExpr(p))
		}
		ret := g.resolveTypeExpr(te.FnType.Return)
		return checker.FuncType(params, ret)
	}
	var base *checker.Type
	switch te.Name {
	case "void":
		base = checker.Void
	case "int":
		base = checker.Int
	case "float":
		base = checker.Float
	case "bool":
		base = checker.Bool
	case "string":
		base = checker.String
	default:
		base = checker.ClassType(te.Name)
	}
	if te.Array() {
		base = checker.ArrayOf(base)
	}
	return base
}

func (g *Generator) fieldIndex(ci *checker.ClassInfo, field string) int32 {
	for i, f := range ci.FieldOrder {
		if f == field {
			return int32(i + 1) // +1 because index 0 is the vtable pointer
		}
	}
	return -1
}

// ── Expression type inference helpers ─────────────────────────────────────────
// These mirror the checker but read from resolved types on expr nodes.

func (g *Generator) exprType(e *ast.Expr) *checker.Type {
	if e == nil {
		return checker.Void
	}
	if t, ok := e.ResolvedType.(*checker.Type); ok && t != nil {
		return t
	}
	return checker.Void
}

func (g *Generator) orExprType(a *ast.OrExpr) *checker.Type {
	return g.andExprType(a.Left)
}

func (g *Generator) andExprType(a *ast.AndExpr) *checker.Type {
	return g.eqExprType(a.Left)
}

func (g *Generator) eqExprType(a *ast.EqExpr) *checker.Type {
	return g.cmpExprType(a.Left)
}

func (g *Generator) cmpExprType(a *ast.CmpExpr) *checker.Type {
	return g.addExprType(a.Left)
}

func (g *Generator) addExprType(a *ast.AddExpr) *checker.Type {
	return g.mulExprType(a.Left)
}

func (g *Generator) mulExprType(a *ast.MulExpr) *checker.Type {
	return g.unaryExprType(a.Left)
}

func (g *Generator) unaryExprType(u *ast.UnaryExpr) *checker.Type {
	if u.Op != "" {
		switch u.Op {
		case "!":
			return checker.Bool
		case "-":
			return g.unaryExprType(u.Expr)
		}
	}
	return g.postfixType(u.Post)
}

func (g *Generator) postfixType(p *ast.PostfixExpr) *checker.Type {
	ty := g.primaryType(p.Base)
	for _, op := range p.Ops {
		switch {
		case op.Call != nil:
			if ty.Kind == checker.KindFunc {
				ty = ty.Return
			} else {
				ty = checker.Void
			}
		case op.Field != "":
			if ty.Kind == checker.KindClass {
				ci := g.info.Classes[ty.ClassName]
				if ci != nil {
					if ft, ok := ci.Fields[op.Field]; ok {
						ty = ft
					} else if mt, ok := ci.Methods[op.Field]; ok {
						ty = mt
					}
				}
			}
		case op.Index != nil:
			if ty.Kind == checker.KindArray {
				ty = ty.Elem
			}
		}
	}
	return ty
}

func (g *Generator) primaryType(p *ast.PrimaryExpr) *checker.Type {
	switch {
	case p.Int != "":
		return checker.Int
	case p.Float != "":
		return checker.Float
	case p.Bool != "":
		return checker.Bool
	case p.String != nil:
		return checker.String
	case p.Null:
		return checker.Null
	case p.This:
		if lv, ok := g.locals["this"]; ok {
			return lv.altType
		}
		return checker.Void
	case p.Ident != "":
		if lv, ok := g.locals[p.Ident]; ok {
			return lv.altType
		}
		if gv, ok := g.globals[p.Ident]; ok {
			return gv.altType
		}
		if ft, ok := g.info.Funcs[p.Ident]; ok {
			return ft
		}
		// Builtin functions: return a FuncType so the Call op case in
		// postfixType can correctly derive the return type via ty.Return.
		if rt, ok := builtinReturnType[p.Ident]; ok {
			return checker.FuncType(nil, rt)
		}
		return checker.Void
	case p.Array != nil:
		if len(p.Array.Elems) > 0 {
			return checker.ArrayOf(g.exprType(p.Array.Elems[0]))
		}
		return checker.ArrayOf(checker.Void)
	case p.FnExpr != nil:
		var params []*checker.Type
		for _, param := range p.FnExpr.Params {
			params = append(params, g.resolveTypeExpr(param.Type))
		}
		return checker.FuncType(params, g.resolveTypeExpr(p.FnExpr.Ret))
	case p.NewExpr != nil:
		return checker.ClassType(p.NewExpr.ClassName)
	case p.Paren != nil:
		return g.exprType(p.Paren)
	}
	return checker.Void
}

// builtinReturnType maps builtin function names to their return types.
// Used by the codegen's type-inference helpers (primaryType etc.) which
// don't have access to the checker's scope.
var builtinReturnType = map[string]*checker.Type{
	"print":           checker.Void,
	"write":           checker.Void,
	"println":         checker.Void,
	"len":             checker.Int,
	"push":            checker.Void,
	"int_to_string":   checker.String,
	"float_to_string": checker.String,
	"char_at":         checker.Int,
	"string_slice":    checker.String,
	"char_to_string":  checker.String,
	"read_file":       checker.String,
	"write_file":      checker.Void,
	"exit":            checker.Void,
	"panic":           checker.Void,
	"each_line":       checker.Void,
	"args":            checker.ArrayOf(checker.String),
	"print_err":       checker.Void,
	"read_stdin":      checker.String,
	"now_millis":      checker.Int,
	"now_seconds":     checker.Int,
	"now_string":      checker.String,
}
