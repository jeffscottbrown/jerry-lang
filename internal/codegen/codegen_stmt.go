package codegen

import (
	"fmt"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
)

// ── Block and statement generation ───────────────────────────────────────────

func (g *Generator) genBlock(b *ast.Block, out *strings.Builder) error {
	if b == nil {
		return nil
	}
	savedLocals := g.cloneLocals()
	g.pushReleaseScope()
	for _, s := range b.Stmts {
		if g.terminated {
			break
		}
		if err := g.genStmt(s, out); err != nil {
			return err
		}
	}
	if !g.terminated {
		g.emitCurrentScopeReleases(out)
	}
	g.popReleaseScope()
	// Restore locals that existed before the block (inner bindings go out of scope).
	g.locals = savedLocals
	return nil
}

func (g *Generator) genStmt(s *ast.StmtNode, out *strings.Builder) error {
	switch {
	case s.VarDecl != nil:
		return g.genVarDecl(s.VarDecl, out)
	case s.Return != nil:
		return g.genReturn(s.Return, out)
	case s.If != nil:
		return g.genIf(s.If, out)
	case s.While != nil:
		return g.genWhile(s.While, out)
	case s.For != nil:
		return g.genFor(s.For, out)
	case s.Break != nil:
		if len(g.labelStack) == 0 {
			return fmt.Errorf("break outside loop")
		}
		loopDepth := g.loopScopeDepth[len(g.loopScopeDepth)-1]
		g.emitLoopBoundaryReleases(loopDepth, out)
		top := g.labelStack[len(g.labelStack)-1]
		fmt.Fprintf(out, "  br label %%%s\n", top.endLabel)
		g.terminated = true
	case s.Continue != nil:
		if len(g.labelStack) == 0 {
			return fmt.Errorf("continue outside loop")
		}
		loopDepth := g.loopScopeDepth[len(g.loopScopeDepth)-1]
		g.emitLoopBoundaryReleases(loopDepth, out)
		top := g.labelStack[len(g.labelStack)-1]
		fmt.Fprintf(out, "  br label %%%s\n", top.condLabel)
		g.terminated = true
	case s.ExprStmt != nil:
		_, err := g.genExpr(s.ExprStmt.Expr, out)
		return err
	}
	return nil
}

func (g *Generator) genVarDecl(vd *ast.VarDecl, out *strings.Builder) error {
	ty := g.exprType(vd.Value)
	// When the initializer is an empty [] or {} literal, exprType returns
	// ArrayOf(Void) or MapOf(Void,Void). Use the explicit annotation instead so
	// downstream element/key/value-type loads get the correct LLVM type.
	if vd.Ann != nil {
		if ty.Kind == checker.KindVoid ||
			(ty.Kind == checker.KindArray && ty.Elem.Kind == checker.KindVoid) ||
			(ty.Kind == checker.KindMap && ty.Key.Kind == checker.KindVoid) {
			ty = g.resolveTypeExpr(vd.Ann)
		}
	}
	var val string
	if lit := bareStringLit(vd.Value); lit != nil {
		// Bare string literal: the named alloca below owns the reference directly;
		// no hidden alloca needed.
		val = g.genStringLit(*lit, out)
	} else {
		var err error
		val, err = g.genExpr(vd.Value, out)
		if err != nil {
			return err
		}
	}
	lt := g.llvmType(ty)
	reg := g.allocaInto(out, vd.Name, lt)
	g.storeInto(out, lt, val, reg)
	g.locals[vd.Name] = &localVar{reg: reg, llvmTy: lt, altType: ty}
	// Only register for release if the initializer is a fresh heap allocation
	// (function call result, new-expr, concat, etc.). A bare variable load is a
	// borrow — the source variable remains the owner and will release it.
	if isHeapType(ty) && simpleIdent(vd.Value) == "" {
		if endsWithIndex(vd.Value) {
			// Array element access returns a borrowed pointer (no implicit retain).
			// Retain here so this variable owns its reference and the paired release
			// at scope exit is balanced.
			fmt.Fprintf(out, "  call void @jerry_retain(ptr %s)\n", val)
		}
		g.registerHeapLocal(reg, ty)
	}
	return nil
}

func (g *Generator) genReturn(r *ast.ReturnStmt, out *strings.Builder) error {
	if r.Value == nil {
		g.emitAllReleases("", out)
		fmt.Fprintf(out, "  ret void\n")
		g.terminated = true
		return nil
	}
	// Bare string literal return: call genStringLit directly so no hidden alloca
	// is created. The raw +1 pointer is transferred to the caller; emitAllReleases
	// won't touch it since it was never registered.
	var val string
	if lit := bareStringLit(r.Value); lit != nil && isHeapType(g.retType) {
		val = g.genStringLit(*lit, out)
		g.emitAllReleases("", out)
		fmt.Fprintf(out, "  ret ptr %s\n", val)
		g.terminated = true
		return nil
	}
	var err error
	val, err = g.genExpr(r.Value, out)
	if err != nil {
		return err
	}
	// If returning a heap-type bare local variable, exempt its alloca from release
	// so the caller receives the reference we were holding (transfer of ownership).
	exemptAllocaReg := ""
	if isHeapType(g.retType) {
		if ident := simpleIdent(r.Value); ident != "" {
			if lv, ok := g.locals[ident]; ok && isHeapType(lv.altType) {
				exemptAllocaReg = lv.reg
			}
		}
	}
	g.emitAllReleases(exemptAllocaReg, out)
	retLLVM := g.llvmType(g.retType)
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, val)
	g.terminated = true
	return nil
}

func (g *Generator) genIf(s *ast.IfStmt, out *strings.Builder) error {
	thenLbl := g.newLabel("if.then")
	elseLbl := g.newLabel("if.else")
	mergeLbl := g.newLabel("if.merge")

	cond, err := g.genExpr(s.Cond, out)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", cond, thenLbl, elseLbl)

	// then
	g.emitBlockLabel(thenLbl, out)
	if err := g.genBlock(s.Then, out); err != nil {
		return err
	}
	if !g.terminated {
		fmt.Fprintf(out, "  br label %%%s\n", mergeLbl)
	}
	thenTerminated := g.terminated

	// else / else-if
	g.emitBlockLabel(elseLbl, out)
	if s.Else != nil {
		if s.Else.ElseIf != nil {
			if err := g.genIf(s.Else.ElseIf, out); err != nil {
				return err
			}
		} else {
			if err := g.genBlock(s.Else.Block, out); err != nil {
				return err
			}
		}
	}
	if !g.terminated {
		fmt.Fprintf(out, "  br label %%%s\n", mergeLbl)
	}
	elseTerminated := g.terminated

	// merge
	g.emitBlockLabel(mergeLbl, out)
	if thenTerminated && elseTerminated {
		// Both branches terminated; merge block is unreachable but must have a terminator.
		fmt.Fprintf(out, "  unreachable\n")
		g.terminated = true
	}
	return nil
}

func (g *Generator) genWhile(s *ast.WhileStmt, out *strings.Builder) error {
	condLbl := g.newLabel("while.cond")
	bodyLbl := g.newLabel("while.body")
	endLbl := g.newLabel("while.end")

	fmt.Fprintf(out, "  br label %%%s\n", condLbl)
	g.emitBlockLabel(condLbl, out)

	cond, err := g.genExpr(s.Cond, out)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLbl, endLbl)

	g.emitBlockLabel(bodyLbl, out)
	g.loopScopeDepth = append(g.loopScopeDepth, len(g.releaseScopes))
	g.labelStack = append(g.labelStack, loopLabels{condLbl, endLbl})
	if err := g.genBlock(s.Body, out); err != nil {
		return err
	}
	g.labelStack = g.labelStack[:len(g.labelStack)-1]
	g.loopScopeDepth = g.loopScopeDepth[:len(g.loopScopeDepth)-1]
	if !g.terminated {
		fmt.Fprintf(out, "  br label %%%s\n", condLbl)
	}

	g.emitBlockLabel(endLbl, out)
	return nil
}

func (g *Generator) genFor(s *ast.ForStmt, out *strings.Builder) error {
	condLbl := g.newLabel("for.cond")
	bodyLbl := g.newLabel("for.body")
	postLbl := g.newLabel("for.post")
	endLbl := g.newLabel("for.end")

	savedLocals := g.cloneLocals()
	g.pushReleaseScope() // scope for for-init heap variables

	// Init
	if s.Init != nil {
		if s.Init.VarDecl != nil {
			vd := s.Init.VarDecl
			val, err := g.genExpr(vd.Value, out)
			if err != nil {
				return err
			}
			ty := g.exprType(vd.Value)
			lt := g.llvmType(ty)
			reg := g.allocaInto(out, vd.Name, lt)
			g.storeInto(out, lt, val, reg)
			g.locals[vd.Name] = &localVar{reg: reg, llvmTy: lt, altType: ty}
			if isHeapType(ty) {
				g.registerHeapLocal(reg, ty)
			}
		} else if s.Init.Expr != nil {
			if _, err := g.genExpr(s.Init.Expr, out); err != nil {
				return err
			}
		}
	}

	// Record depth after for-init scope so break/continue release body scopes only;
	// the for-init scope is released at endLbl (persists across iterations).
	g.loopScopeDepth = append(g.loopScopeDepth, len(g.releaseScopes))

	fmt.Fprintf(out, "  br label %%%s\n", condLbl)
	g.emitBlockLabel(condLbl, out)

	// Condition
	if s.Cond != nil {
		cond, err := g.genExpr(s.Cond, out)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLbl, endLbl)
	} else {
		fmt.Fprintf(out, "  br label %%%s\n", bodyLbl)
	}

	// Body
	g.emitBlockLabel(bodyLbl, out)
	g.labelStack = append(g.labelStack, loopLabels{postLbl, endLbl})
	if err := g.genBlock(s.Body, out); err != nil {
		return err
	}
	g.labelStack = g.labelStack[:len(g.labelStack)-1]
	if !g.terminated {
		fmt.Fprintf(out, "  br label %%%s\n", postLbl)
	}

	// Post
	g.emitBlockLabel(postLbl, out)
	if s.Post != nil {
		if _, err := g.genExpr(s.Post, out); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "  br label %%%s\n", condLbl)

	// End: release any for-init heap variables, then pop the scope.
	g.emitBlockLabel(endLbl, out)
	g.emitCurrentScopeReleases(out)
	g.popReleaseScope()
	g.loopScopeDepth = g.loopScopeDepth[:len(g.loopScopeDepth)-1]
	g.locals = savedLocals
	return nil
}

// genGlobalInitFn generates the jerry_global_init() function that stores the
// real initial values into all top-level variables.
func (g *Generator) genGlobalInitFn(varDecls []*ast.VarDecl, fnBuf *strings.Builder) error {
	fmt.Fprintf(fnBuf, "define void @jerry_global_init() {\n")
	fmt.Fprintf(fnBuf, "entry:\n")

	saved := g.saveContext()
	g.curFnName = "jerry_global_init"
	g.curBlock = "entry"
	g.retType = checker.Void
	g.locals = make(map[string]*localVar)
	g.terminated = false

	for _, vd := range varDecls {
		val, err := g.genExpr(vd.Value, fnBuf)
		if err != nil {
			return err
		}
		gvar := g.globals[vd.Name]
		fmt.Fprintf(fnBuf, "  store %s %s, ptr %s\n", gvar.llvmTy, val, gvar.reg)
	}

	fmt.Fprintf(fnBuf, "  ret void\n")
	fmt.Fprintf(fnBuf, "}\n\n")
	g.restoreContext(saved)
	return nil
}
