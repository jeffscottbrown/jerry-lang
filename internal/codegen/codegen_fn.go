package codegen

import (
	"fmt"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
)

// ── Function code generation ──────────────────────────────────────────────────

func (g *Generator) genFnDecl(fn *ast.FnDecl, out *strings.Builder) error {
	ft := g.info.Funcs[fn.Name]
	if ft == nil {
		return fmt.Errorf("function %q not in type info", fn.Name)
	}

	llvmName := fn.Name + "_jerry"
	if fn.Name == "main" {
		llvmName = "main_jerry"
	}

	// Build parameter list
	var params []string
	paramNames := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		lt := g.llvmType(ft.Params[i])
		reg := "%" + p.Name + ".arg"
		params = append(params, lt+" "+reg)
		paramNames[i] = reg
	}

	retLLVM := g.llvmType(ft.Return)
	fmt.Fprintf(out, "define %s @%s(%s) {\n", retLLVM, llvmName, strings.Join(params, ", "))
	fmt.Fprintf(out, "entry:\n")

	saved := g.saveContext()
	g.curFnName = llvmName
	g.curBlock = "entry"
	g.retType = ft.Return
	g.locals = make(map[string]*localVar)
	g.terminated = false

	// Allocate locals for parameters so they're mutable.
	for i, p := range fn.Params {
		lt := g.llvmType(ft.Params[i])
		reg := g.allocaInto(out, p.Name, lt)
		g.storeInto(out, lt, paramNames[i], reg)
		g.locals[p.Name] = &localVar{reg: reg, llvmTy: lt, altType: ft.Params[i]}
	}

	if err := g.genBlock(fn.Body, out); err != nil {
		return err
	}

	// Emit default return if block didn't terminate.
	if !g.terminated {
		if retLLVM == "void" {
			fmt.Fprintf(out, "  ret void\n")
		} else {
			fmt.Fprintf(out, "  ret %s %s\n", retLLVM, g.zeroValue(ft.Return))
		}
	}

	fmt.Fprintf(out, "}\n\n")
	g.restoreContext(saved)
	return nil
}

// genFnExpr generates an anonymous function (closure) expression.
// The function is emitted at module scope and a closure struct is returned.
func (g *Generator) genFnExpr(fn *ast.FnExpr, out *strings.Builder) (string, error) {
	fnID := g.newTmp()[1:] // strip leading %
	fnName := "anon_" + fnID + "_jerry"

	var params []string
	params = append(params, "ptr %env.arg") // env pointer (unused for non-closures)
	paramNames := []string{"%env.arg"}
	var paramTypes []*checker.Type
	for _, p := range fn.Params {
		pt := g.resolveTypeExpr(p.Type)
		lt := g.llvmType(pt)
		reg := "%" + p.Name + ".arg"
		params = append(params, lt+" "+reg)
		paramNames = append(paramNames, reg)
		paramTypes = append(paramTypes, pt)
	}
	retTy := g.resolveTypeExpr(fn.Ret)
	retLLVM := g.llvmType(retTy)

	// Write the function to a side buffer so it appears before the call site.
	var fnOut strings.Builder
	fmt.Fprintf(&fnOut, "define private %s @%s(%s) {\n", retLLVM, fnName, strings.Join(params, ", "))
	fmt.Fprintf(&fnOut, "entry:\n")

	savedCtx := g.saveContext()
	g.curFnName = fnName
	g.curBlock = "entry"
	g.retType = retTy
	g.locals = make(map[string]*localVar)
	g.terminated = false

	for i, p := range fn.Params {
		lt := g.llvmType(paramTypes[i])
		reg := g.allocaInto(&fnOut, p.Name, lt)
		g.storeInto(&fnOut, lt, paramNames[i+1], reg)
		g.locals[p.Name] = &localVar{reg: reg, llvmTy: lt, altType: paramTypes[i]}
	}
	if err := g.genBlock(fn.Body, &fnOut); err != nil {
		return "", err
	}
	if !g.terminated {
		if retLLVM == "void" {
			fmt.Fprintf(&fnOut, "  ret void\n")
		} else {
			fmt.Fprintf(&fnOut, "  ret %s %s\n", retLLVM, g.zeroValue(retTy))
		}
	}
	fmt.Fprintf(&fnOut, "}\n\n")

	g.restoreContext(savedCtx)

	// Emit the anonymous function at module level, not inline.
	g.pendingFns.WriteString(fnOut.String())

	// Create a closure struct: { fn_ptr, null env }
	closureReg := g.newTmp()
	fmt.Fprintf(out, "  %s = call ptr @jerry_alloc(i64 16)\n", closureReg)
	fptrSlot := g.newTmp()
	fmt.Fprintf(out, "  %s = getelementptr %%JerryClosure, ptr %s, i32 0, i32 0\n", fptrSlot, closureReg)
	fmt.Fprintf(out, "  store ptr @%s, ptr %s\n", fnName, fptrSlot)
	envSlot := g.newTmp()
	fmt.Fprintf(out, "  %s = getelementptr %%JerryClosure, ptr %s, i32 0, i32 1\n", envSlot, closureReg)
	fmt.Fprintf(out, "  store ptr null, ptr %s\n", envSlot)

	return closureReg, nil
}
