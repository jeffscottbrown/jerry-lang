// Package codegen emits LLVM IR (textual form) from a type-checked Jerry AST.
// The generated IR is intended to be compiled with clang on x86-64 macOS.
package codegen

import (
	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
	"fmt"
	"strconv"
	"strings"
)

// ── Generator ─────────────────────────────────────────────────────────────────

type Generator struct {
	out  strings.Builder
	info *checker.Info

	// Counter for unique temporaries and labels
	tmp   int
	lbl   int
	strID int

	// String constants accumulated and emitted at start
	strConsts []string

	// Class struct type declarations
	classTypes strings.Builder

	// Anonymous functions (closures) generated while inside another function.
	// Must be emitted at module level, not inline.
	pendingFns strings.Builder

	// Current function context
	curFnName string
	retType   *checker.Type
	locals    map[string]*localVar // name → alloca register + type
	labelStack []loopLabels         // for break/continue

	// Tracks whether current basic block has a terminator
	terminated bool

	// Name of the current basic block (without %)
	curBlock string
}

type localVar struct {
	reg     string // alloca register, e.g. "%x.1"
	llvmTy  string // LLVM type string, e.g. "i64"
	altType *checker.Type
}

type loopLabels struct {
	condLabel string
	endLabel  string
}

// ── Entry point ───────────────────────────────────────────────────────────────

// Generate compiles a set of programs (core + stdlib + project files) into a
// single LLVM IR module. The programs must already be type-checked and their
// type info collected into info.
func Generate(progs []*ast.Program, info *checker.Info) (string, error) {
	g := &Generator{info: info}
	if err := g.genPrograms(progs); err != nil {
		return "", err
	}
	return g.out.String(), nil
}

func (g *Generator) genPrograms(progs []*ast.Program) error {
	// We buffer class type decls and string constants, emitting them after
	// collecting everything.
	var fnBuf strings.Builder
	g.out = strings.Builder{} // will be rebuilt

	// Build class type declarations (uses g.info which has all classes).
	g.emitClassTypeDecls()

	// Generate function bodies from all programs in order.
	for _, prog := range progs {
		for _, tl := range prog.Stmts {
			switch {
			case tl.FnDecl != nil:
				if err := g.genFnDecl(tl.FnDecl, &fnBuf); err != nil {
					return err
				}
			case tl.Class != nil:
				if err := g.genClassDecl(tl.Class, &fnBuf); err != nil {
					return err
				}
			case tl.VarDecl != nil:
				return fmt.Errorf("top-level variable declarations not yet supported")
			// tl.Include: no IR to emit
			}
		}
	}

	// Now assemble the final module.
	var mod strings.Builder
	mod.WriteString(g.moduleHeader())
	mod.WriteString(g.classTypes.String())
	mod.WriteString(g.runtimeDecls())
	// Emit string constants.
	for i, s := range g.strConsts {
		escaped := llvmEscapeString(s)
		n := len(s) + 1 // +1 for null terminator
		fmt.Fprintf(&mod, "@.str.%d = private unnamed_addr constant [%d x i8] c\"%s\\00\"\n", i, n, escaped)
	}
	mod.WriteString("\n")
	mod.WriteString(g.classVtableDecls())
	mod.WriteString(g.pendingFns.String()) // anonymous functions (closures)
	mod.WriteString(fnBuf.String())
	mod.WriteString(g.cMainWrapper())
	g.out = mod
	return nil
}

// ── Module header and runtime declarations ────────────────────────────────────

func (g *Generator) moduleHeader() string {
	return `; Jerry compiled module
; target triple is intentionally omitted — clang sets it from the host

; ── Jerry runtime types ──────────────────────────────────────────────────────
; JerryStr  = { i64 len, ptr data }
; JerryArray = { i64 len, i64 cap, ptr data, i64 elem_size }
%JerryStr   = type { i64, ptr }
%JerryArray = type { i64, i64, ptr, i64 }
; JerryClosure = { ptr fn_ptr, ptr env_ptr }
%JerryClosure = type { ptr, ptr }

`
}

func (g *Generator) runtimeDecls() string {
	return `; ── Runtime function declarations ────────────────────────────────────────────
declare ptr  @jerry_string_new(ptr, i64)
declare ptr  @jerry_string_concat(ptr, ptr)
declare i8   @jerry_string_eq(ptr, ptr)
declare i8   @jerry_string_ne(ptr, ptr)
declare i64  @jerry_string_len(ptr)
declare ptr  @jerry_int_to_string(i64)
declare ptr  @jerry_float_to_string(double)
declare i64  @jerry_char_at(ptr, i64)
declare ptr  @jerry_string_slice(ptr, i64, i64)
declare ptr  @jerry_char_to_string(i64)
declare void @jerry_print_int(i64)
declare void @jerry_print_float(double)
declare void @jerry_print_bool(i8)
declare void @jerry_print_string(ptr)
declare void @jerry_print_array(ptr)
declare void @jerry_println()
declare ptr  @jerry_array_new(i64, i64)
declare ptr  @jerry_array_get(ptr, i64)
declare void @jerry_array_set(ptr, i64, ptr)
declare i64  @jerry_array_len(ptr)
declare void @jerry_array_push(ptr, ptr)
declare void @jerry_panic(ptr)
declare ptr  @jerry_alloc(i64)
declare ptr  @jerry_read_file(ptr)
declare void @jerry_write_file(ptr, ptr)

`
}

// ── Class type declarations ───────────────────────────────────────────────────

func (g *Generator) emitClassTypeDecls() {
	for name, ci := range g.info.Classes {
		// Struct: { ptr vtable, field0, field1, ... }
		var fields []string
		fields = append(fields, "ptr") // vtable pointer
		for _, fname := range ci.FieldOrder {
			fields = append(fields, g.llvmType(ci.Fields[fname]))
		}
		fmt.Fprintf(&g.classTypes, "%%%s = type { %s }\n", name, strings.Join(fields, ", "))
	}
	if len(g.info.Classes) > 0 {
		g.classTypes.WriteString("\n")
	}
}

func (g *Generator) classVtableDecls() string {
	var sb strings.Builder
	for name, ci := range g.info.Classes {
		if len(ci.MethodOrder) == 0 {
			continue
		}
		// Emit vtable as a global constant array of pointers.
		var ptrs []string
		for _, mname := range ci.MethodOrder {
			llvmFn := g.methodFnName(name, mname)
			ptrs = append(ptrs, "ptr @"+llvmFn)
		}
		fmt.Fprintf(&sb, "@vtable.%s = private constant [%d x ptr] [%s]\n",
			name, len(ptrs), strings.Join(ptrs, ", "))
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	return sb.String()
}

func (g *Generator) cMainWrapper() string {
	return `
; ── C entry point ────────────────────────────────────────────────────────────
define i32 @main(i32 %argc, ptr %argv) {
entry:
  call void @main_jerry()
  ret i32 0
}
`
}

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

	// Set up generator context for this function.
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

// ── Class code generation ─────────────────────────────────────────────────────

func (g *Generator) genClassDecl(cl *ast.ClassDecl, out *strings.Builder) error {
	ci := g.info.Classes[cl.Name]
	if ci == nil {
		return fmt.Errorf("class %q not in type info", cl.Name)
	}

	for _, m := range cl.Members {
		if m.Method == nil {
			continue
		}
		fn := m.Method
		mt := ci.Methods[fn.Name]
		if mt == nil {
			continue
		}

		llvmName := g.methodFnName(cl.Name, fn.Name)

		// Parameters: self ptr + declared params
		var params []string
		params = append(params, "ptr %self.arg")
		paramNames := []string{"%self.arg"}
		for i, p := range fn.Params {
			lt := g.llvmType(mt.Params[i])
			reg := "%" + p.Name + ".arg"
			params = append(params, lt+" "+reg)
			paramNames = append(paramNames, reg)
		}

		retLLVM := g.llvmType(mt.Return)
		fmt.Fprintf(out, "define %s @%s(%s) {\n", retLLVM, llvmName, strings.Join(params, ", "))
		fmt.Fprintf(out, "entry:\n")

		saved := g.saveContext()
		g.curFnName = llvmName
		g.curBlock = "entry"
		g.retType = mt.Return
		g.locals = make(map[string]*localVar)
		g.terminated = false

		// 'this' refers to self
		selfReg := g.allocaInto(out, "this", "ptr")
		g.storeInto(out, "ptr", "%self.arg", selfReg)
		g.locals["this"] = &localVar{reg: selfReg, llvmTy: "ptr",
			altType: checker.ClassType(cl.Name)}

		// Parameters
		for i, p := range fn.Params {
			lt := g.llvmType(mt.Params[i])
			reg := g.allocaInto(out, p.Name, lt)
			g.storeInto(out, lt, paramNames[i+1], reg)
			g.locals[p.Name] = &localVar{reg: reg, llvmTy: lt, altType: mt.Params[i]}
		}

		if err := g.genBlock(fn.Body, out); err != nil {
			return err
		}
		if !g.terminated {
			if retLLVM == "void" {
				fmt.Fprintf(out, "  ret void\n")
			} else {
				fmt.Fprintf(out, "  ret %s %s\n", retLLVM, g.zeroValue(mt.Return))
			}
		}
		fmt.Fprintf(out, "}\n\n")
		g.restoreContext(saved)
	}
	return nil
}

func (g *Generator) methodFnName(class, method string) string {
	return class + "_" + method + "_jerry"
}

// ── Block and statement generation ───────────────────────────────────────────

func (g *Generator) genBlock(b *ast.Block, out *strings.Builder) error {
	if b == nil {
		return nil
	}
	savedLocals := g.cloneLocals()
	for _, s := range b.Stmts {
		if g.terminated {
			break
		}
		if err := g.genStmt(s, out); err != nil {
			return err
		}
	}
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
		top := g.labelStack[len(g.labelStack)-1]
		fmt.Fprintf(out, "  br label %%%s\n", top.endLabel)
		g.terminated = true
	case s.Continue != nil:
		if len(g.labelStack) == 0 {
			return fmt.Errorf("continue outside loop")
		}
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
	val, err := g.genExpr(vd.Value, out)
	if err != nil {
		return err
	}
	ty := g.exprType(vd.Value)
	lt := g.llvmType(ty)
	reg := g.allocaInto(out, vd.Name, lt)
	g.storeInto(out, lt, val, reg)
	g.locals[vd.Name] = &localVar{reg: reg, llvmTy: lt, altType: ty}
	return nil
}

func (g *Generator) genReturn(r *ast.ReturnStmt, out *strings.Builder) error {
	if r.Value == nil {
		fmt.Fprintf(out, "  ret void\n")
		g.terminated = true
		return nil
	}
	val, err := g.genExpr(r.Value, out)
	if err != nil {
		return err
	}
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
	g.terminated = thenTerminated && elseTerminated
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
	g.labelStack = append(g.labelStack, loopLabels{condLbl, endLbl})
	if err := g.genBlock(s.Body, out); err != nil {
		return err
	}
	g.labelStack = g.labelStack[:len(g.labelStack)-1]
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
		} else if s.Init.Expr != nil {
			if _, err := g.genExpr(s.Init.Expr, out); err != nil {
				return err
			}
		}
	}

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

	g.emitBlockLabel(endLbl, out)
	g.locals = savedLocals
	return nil
}

// ── Expression generation ─────────────────────────────────────────────────────
// Each genExpr returns the LLVM register/value holding the result.

func (g *Generator) genExpr(e *ast.Expr, out *strings.Builder) (string, error) {
	if e == nil {
		return "void", nil
	}
	return g.genAssign(e.Assignment, out)
}

func (g *Generator) genAssign(a *ast.AssignExpr, out *strings.Builder) (string, error) {
	if a.Right == nil {
		return g.genOr(a.Left, out)
	}
	// Assignment: compute RHS first, then store to LHS lvalue.
	rhs, err := g.genAssign(a.Right, out)
	if err != nil {
		return "", err
	}
	rhsTy := g.orExprType(a.Left)
	lt := g.llvmType(rhsTy)
	if err := g.genStore(a.Left, lt, rhs, out); err != nil {
		return "", err
	}
	return rhs, nil
}

// genStore writes a value to an lvalue expression.
func (g *Generator) genStore(lv *ast.OrExpr, lt, rhs string, out *strings.Builder) error {
	// lvalue must be: ident, or postfix ending with .field or [index]
	if lv.Left == nil || len(lv.Rest) != 0 {
		return fmt.Errorf("invalid assignment target")
	}
	and := lv.Left
	if len(and.Rest) != 0 {
		return fmt.Errorf("invalid assignment target")
	}
	eq := and.Left
	if len(eq.Rest) != 0 {
		return fmt.Errorf("invalid assignment target")
	}
	cmp := eq.Left
	if len(cmp.Rest) != 0 {
		return fmt.Errorf("invalid assignment target")
	}
	add := cmp.Left
	if len(add.Rest) != 0 {
		return fmt.Errorf("invalid assignment target")
	}
	mul := add.Left
	if len(mul.Rest) != 0 {
		return fmt.Errorf("invalid assignment target")
	}
	unary := mul.Left
	if unary.Op != "" {
		return fmt.Errorf("invalid assignment target")
	}
	post := unary.Post
	if len(post.Ops) == 0 {
		// Simple ident assignment
		prim := post.Base
		if prim.Ident == "" {
			return fmt.Errorf("invalid assignment target")
		}
		lvar, ok := g.locals[prim.Ident]
		if !ok {
			return fmt.Errorf("undefined variable %q", prim.Ident)
		}
		g.storeInto(out, lt, rhs, lvar.reg)
		return nil
	}
	// Postfix: could be obj.field or arr[idx] — build the base then store.
	lastOp := post.Ops[len(post.Ops)-1]
	// Evaluate up to but not including the last op.
	basePost := &ast.PostfixExpr{Base: post.Base, Ops: post.Ops[:len(post.Ops)-1]}
	baseVal, baseTy, err := g.genPostfixVal(basePost, out)
	if err != nil {
		return err
	}

	switch {
	case lastOp.Field != "":
		if baseTy.Kind != checker.KindClass {
			return fmt.Errorf("field assignment on non-class")
		}
		ci := g.info.Classes[baseTy.ClassName]
		fieldIdx := g.fieldIndex(ci, lastOp.Field)
		gepReg := g.newTmp()
		fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr %s, i32 0, i32 %d\n",
			gepReg, baseTy.ClassName, baseVal, fieldIdx)
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", lt, rhs, gepReg)
	case lastOp.Index != nil:
		if baseTy.Kind != checker.KindArray {
			return fmt.Errorf("index assignment on non-array")
		}
		idxVal, err := g.genExpr(lastOp.Index, out)
		if err != nil {
			return err
		}
		// Allocate a temporary to hold the value for array_set
		tmpAlloca := g.newTmp() + ".slot"
		fmt.Fprintf(out, "  %s = alloca %s\n", tmpAlloca, lt)
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", lt, rhs, tmpAlloca)
		fmt.Fprintf(out, "  call void @jerry_array_set(ptr %s, i64 %s, ptr %s)\n",
			baseVal, idxVal, tmpAlloca)
	default:
		return fmt.Errorf("invalid assignment target")
	}
	return nil
}

func (g *Generator) genOr(a *ast.OrExpr, out *strings.Builder) (string, error) {
	// Short-circuit: if left is true, skip right.
	val, err := g.genAnd(a.Left, out)
	if err != nil {
		return "", err
	}
	for _, r := range a.Rest {
		lhsBlock := g.curBlock // block where lhs was last defined
		trueLabel := g.newLabel("or.true")
		rightLabel := g.newLabel("or.rhs")
		mergeLabel := g.newLabel("or.merge")
		// If left is true, jump straight to merge (with true); otherwise evaluate right.
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", val, trueLabel, rightLabel)
		g.emitBlockLabel(rightLabel, out)
		rval, err := g.genAnd(r.Right, out)
		if err != nil {
			return "", err
		}
		rhsEndBlock := g.curBlock // actual block rval is defined in
		fmt.Fprintf(out, "  br label %%%s\n", mergeLabel)
		g.emitBlockLabel(trueLabel, out)
		fmt.Fprintf(out, "  br label %%%s\n", mergeLabel)
		g.emitBlockLabel(mergeLabel, out)
		res := g.newTmp()
		// lhsBlock may not be the direct predecessor of trueLabel if we had nested &&/||;
		// trueLabel is always the direct predecessor of mergeLabel for the true path.
		_ = lhsBlock
		fmt.Fprintf(out, "  %s = phi i1 [ true, %%%s ], [ %s, %%%s ]\n", res, trueLabel, rval, rhsEndBlock)
		val = res
	}
	return val, nil
}

func (g *Generator) genAnd(a *ast.AndExpr, out *strings.Builder) (string, error) {
	// Short-circuit: if left is false, skip right.
	val, err := g.genEq(a.Left, out)
	if err != nil {
		return "", err
	}
	for _, r := range a.Rest {
		falseLabel := g.newLabel("and.false")
		rightLabel := g.newLabel("and.rhs")
		mergeLabel := g.newLabel("and.merge")
		// If left is true, evaluate right; otherwise jump to false.
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", val, rightLabel, falseLabel)
		g.emitBlockLabel(rightLabel, out)
		rval, err := g.genEq(r.Right, out)
		if err != nil {
			return "", err
		}
		rhsEndBlock := g.curBlock // actual block rval is defined in
		fmt.Fprintf(out, "  br label %%%s\n", mergeLabel)
		g.emitBlockLabel(falseLabel, out)
		fmt.Fprintf(out, "  br label %%%s\n", mergeLabel)
		g.emitBlockLabel(mergeLabel, out)
		res := g.newTmp()
		fmt.Fprintf(out, "  %s = phi i1 [ %s, %%%s ], [ false, %%%s ]\n", res, rval, rhsEndBlock, falseLabel)
		val = res
	}
	return val, nil
}

func (g *Generator) genEq(a *ast.EqExpr, out *strings.Builder) (string, error) {
	val, err := g.genCmp(a.Left, out)
	if err != nil {
		return "", err
	}
	lty := g.cmpExprType(a.Left)
	for _, r := range a.Rest {
		rval, err := g.genCmp(r.Right, out)
		if err != nil {
			return "", err
		}
		res := g.newTmp()
		switch lty.Kind {
		case checker.KindInt:
			pred := map[string]string{"==": "eq", "!=": "ne"}[r.Op]
			fmt.Fprintf(out, "  %s = icmp %s i64 %s, %s\n", res, pred, val, rval)
		case checker.KindBool:
			pred := map[string]string{"==": "eq", "!=": "ne"}[r.Op]
			fmt.Fprintf(out, "  %s = icmp %s i1 %s, %s\n", res, pred, val, rval)
		case checker.KindFloat:
			pred := map[string]string{"==": "oeq", "!=": "one"}[r.Op]
			fmt.Fprintf(out, "  %s = fcmp %s double %s, %s\n", res, pred, val, rval)
		case checker.KindString:
			if r.Op == "==" {
				fmt.Fprintf(out, "  %s = call i8 @jerry_string_eq(ptr %s, ptr %s)\n", res, val, rval)
			} else {
				fmt.Fprintf(out, "  %s = call i8 @jerry_string_ne(ptr %s, ptr %s)\n", res, val, rval)
			}
			boolRes := g.newTmp()
			fmt.Fprintf(out, "  %s = icmp ne i8 %s, 0\n", boolRes, res)
			res = boolRes
		default:
			pred := map[string]string{"==": "eq", "!=": "ne"}[r.Op]
			fmt.Fprintf(out, "  %s = icmp %s ptr %s, %s\n", res, pred, val, rval)
		}
		val = res
		lty = checker.Bool
	}
	return val, nil
}

func (g *Generator) genCmp(a *ast.CmpExpr, out *strings.Builder) (string, error) {
	val, err := g.genAdd(a.Left, out)
	if err != nil {
		return "", err
	}
	lty := g.addExprType(a.Left)
	for _, r := range a.Rest {
		rval, err := g.genAdd(r.Right, out)
		if err != nil {
			return "", err
		}
		res := g.newTmp()
		intPred := map[string]string{"<": "slt", "<=": "sle", ">": "sgt", ">=": "sge"}
		fltPred := map[string]string{"<": "olt", "<=": "ole", ">": "ogt", ">=": "oge"}
		switch lty.Kind {
		case checker.KindInt:
			fmt.Fprintf(out, "  %s = icmp %s i64 %s, %s\n", res, intPred[r.Op], val, rval)
		case checker.KindFloat:
			fmt.Fprintf(out, "  %s = fcmp %s double %s, %s\n", res, fltPred[r.Op], val, rval)
		default:
			fmt.Fprintf(out, "  %s = icmp %s i64 %s, %s\n", res, intPred[r.Op], val, rval)
		}
		val = res
		lty = checker.Bool
	}
	return val, nil
}

func (g *Generator) genAdd(a *ast.AddExpr, out *strings.Builder) (string, error) {
	val, err := g.genMul(a.Left, out)
	if err != nil {
		return "", err
	}
	lty := g.mulExprType(a.Left)
	for _, r := range a.Rest {
		rval, err := g.genMul(r.Right, out)
		if err != nil {
			return "", err
		}
		res := g.newTmp()
		switch {
		case r.Op == "+" && lty.Kind == checker.KindString:
			fmt.Fprintf(out, "  %s = call ptr @jerry_string_concat(ptr %s, ptr %s)\n", res, val, rval)
		case lty.Kind == checker.KindFloat:
			op := map[string]string{"+": "fadd", "-": "fsub"}[r.Op]
			fmt.Fprintf(out, "  %s = %s double %s, %s\n", res, op, val, rval)
		default:
			op := map[string]string{"+": "add", "-": "sub"}[r.Op]
			fmt.Fprintf(out, "  %s = %s i64 %s, %s\n", res, op, val, rval)
		}
		val = res
	}
	return val, nil
}

func (g *Generator) genMul(a *ast.MulExpr, out *strings.Builder) (string, error) {
	val, err := g.genUnary(a.Left, out)
	if err != nil {
		return "", err
	}
	lty := g.unaryExprType(a.Left)
	for _, r := range a.Rest {
		rval, err := g.genUnary(r.Right, out)
		if err != nil {
			return "", err
		}
		res := g.newTmp()
		if lty.Kind == checker.KindFloat {
			op := map[string]string{"*": "fmul", "/": "fdiv"}[r.Op]
			fmt.Fprintf(out, "  %s = %s double %s, %s\n", res, op, val, rval)
		} else {
			op := map[string]string{"*": "mul", "/": "sdiv", "%": "srem"}[r.Op]
			fmt.Fprintf(out, "  %s = %s i64 %s, %s\n", res, op, val, rval)
		}
		val = res
	}
	return val, nil
}

func (g *Generator) genUnary(u *ast.UnaryExpr, out *strings.Builder) (string, error) {
	if u.Op == "" {
		return g.genPostfix(u.Post, out)
	}
	val, err := g.genUnary(u.Expr, out)
	if err != nil {
		return "", err
	}
	res := g.newTmp()
	ty := g.unaryExprType(u.Expr)
	switch u.Op {
	case "!":
		fmt.Fprintf(out, "  %s = xor i1 %s, true\n", res, val)
	case "-":
		if ty.Kind == checker.KindFloat {
			fmt.Fprintf(out, "  %s = fneg double %s\n", res, val)
		} else {
			fmt.Fprintf(out, "  %s = sub i64 0, %s\n", res, val)
		}
	}
	return res, nil
}

func (g *Generator) genPostfix(p *ast.PostfixExpr, out *strings.Builder) (string, error) {
	val, ty, err := g.genPostfixVal(p, out)
	_ = ty
	return val, err
}

func (g *Generator) genPostfixVal(p *ast.PostfixExpr, out *strings.Builder) (string, *checker.Type, error) {
	val, err := g.genPrimary(p.Base, out)
	if err != nil {
		return "", nil, err
	}
	ty := g.primaryType(p.Base)

	// Use an explicit index loop so modifications to p.Ops are visible immediately.
	for i := 0; i < len(p.Ops); i++ {
		op := p.Ops[i]
		switch {
		case op.Call != nil:
			var callErr error
			var callVal string
			callVal, ty, callErr = g.genCall(p.Base, val, ty, op.Call, i == 0, out)
			if callErr != nil {
				return "", nil, callErr
			}
			val = callVal

		case op.Field != "":
			if ty.Kind != checker.KindClass {
				return "", nil, fmt.Errorf("field access on non-class type %s", ty)
			}
			ci := g.info.Classes[ty.ClassName]
			if _, isMethod := ci.Methods[op.Field]; isMethod {
				// Look ahead: if the very next op is a call, emit the method call now
				// and skip the Call op by incrementing i an extra time.
				if i+1 < len(p.Ops) && p.Ops[i+1].Call != nil {
					callOp := p.Ops[i+1]
					i++ // consume the Call op
					mval, mty, merr := g.genMethodCall(ty.ClassName, op.Field, val, callOp.Call.Args, out)
					if merr != nil {
						return "", nil, merr
					}
					val = mval
					ty = mty
				} else {
					return "", nil, fmt.Errorf("method %q used as value is not yet supported; call it: obj.%s()", op.Field, op.Field)
				}
			} else if _, isField := ci.Fields[op.Field]; isField {
				fieldIdx := g.fieldIndex(ci, op.Field)
				gepReg := g.newTmp()
				fieldTy := ci.Fields[op.Field]
				fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr %s, i32 0, i32 %d\n",
					gepReg, ty.ClassName, val, fieldIdx)
				loadReg := g.newTmp()
				fmt.Fprintf(out, "  %s = load %s, ptr %s\n",
					loadReg, g.llvmType(fieldTy), gepReg)
				val = loadReg
				ty = fieldTy
			}

		case op.Index != nil:
			if ty.Kind != checker.KindArray {
				return "", nil, fmt.Errorf("index on non-array")
			}
			idxVal, err := g.genExpr(op.Index, out)
			if err != nil {
				return "", nil, err
			}
			ptrReg := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_array_get(ptr %s, i64 %s)\n",
				ptrReg, val, idxVal)
			loadReg := g.newTmp()
			elemTy := ty.Elem
			fmt.Fprintf(out, "  %s = load %s, ptr %s\n",
				loadReg, g.llvmType(elemTy), ptrReg)
			val = loadReg
			ty = elemTy

		case op.PlusPlus, op.MinusMinus:
			varName := p.Base.Ident
			lvar, ok := g.locals[varName]
			if !ok {
				return "", nil, fmt.Errorf("undefined variable %q for ++/--", varName)
			}
			res := g.newTmp()
			if ty.Kind == checker.KindFloat {
				if op.PlusPlus {
					fmt.Fprintf(out, "  %s = fadd double %s, 1.0\n", res, val)
				} else {
					fmt.Fprintf(out, "  %s = fsub double %s, 1.0\n", res, val)
				}
			} else {
				if op.PlusPlus {
					fmt.Fprintf(out, "  %s = add i64 %s, 1\n", res, val)
				} else {
					fmt.Fprintf(out, "  %s = sub i64 %s, 1\n", res, val)
				}
			}
			g.storeInto(out, lvar.llvmTy, res, lvar.reg)
			val = res
		}
	}
	return val, ty, nil
}

// genCall handles function/method call code generation.
// prevBase is the primary expression node (for builtin detection).
// prevVal is the receiver value when calling a method.
func (g *Generator) genCall(
	base *ast.PrimaryExpr,
	calleeVal string,
	calleeTy *checker.Type,
	call *ast.CallArgs,
	isDirectBase bool,
	out *strings.Builder,
) (string, *checker.Type, error) {

	// ── Builtins ──────────────────────────────────────────────────────────────
	if isDirectBase && base != nil && base.Ident != "" {
		switch base.Ident {
		case "print", "write":
			if len(call.Args) != 1 {
				return "", nil, fmt.Errorf("%s() takes 1 argument", base.Ident)
			}
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			argTy := g.exprType(call.Args[0])
			noNewline := base.Ident == "write"
			g.emitPrint(argVal, argTy, noNewline, out)
			return "0", checker.Void, nil

		case "println":
			fmt.Fprintf(out, "  call void @jerry_println()\n")
			return "0", checker.Void, nil

		case "len":
			if len(call.Args) != 1 {
				return "", nil, fmt.Errorf("len() takes 1 argument")
			}
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			argTy := g.exprType(call.Args[0])
			res := g.newTmp()
			if argTy.Kind == checker.KindString {
				fmt.Fprintf(out, "  %s = call i64 @jerry_string_len(ptr %s)\n", res, argVal)
			} else {
				fmt.Fprintf(out, "  %s = call i64 @jerry_array_len(ptr %s)\n", res, argVal)
			}
			return res, checker.Int, nil

		case "push":
			if len(call.Args) != 2 {
				return "", nil, fmt.Errorf("push() takes 2 arguments")
			}
			arrVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			elemVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			elemTy := g.exprType(call.Args[1])
			lt := g.llvmType(elemTy)
			tmpSlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", tmpSlot, lt)
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", lt, elemVal, tmpSlot)
			fmt.Fprintf(out, "  call void @jerry_array_push(ptr %s, ptr %s)\n", arrVal, tmpSlot)
			return "0", checker.Void, nil

		case "int_to_string":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			argVal = g.coerceToI64(argVal, g.exprType(call.Args[0]), out)
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_int_to_string(i64 %s)\n", res, argVal)
			return res, checker.String, nil

		case "float_to_string":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_float_to_string(double %s)\n", res, argVal)
			return res, checker.String, nil

		case "char_at":
			sVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			iVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			iVal = g.coerceToI64(iVal, g.exprType(call.Args[1]), out)
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call i64 @jerry_char_at(ptr %s, i64 %s)\n", res, sVal, iVal)
			return res, checker.Int, nil

		case "string_slice":
			sVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			startVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			startVal = g.coerceToI64(startVal, g.exprType(call.Args[1]), out)
			endVal, err := g.genExpr(call.Args[2], out)
			if err != nil {
				return "", nil, err
			}
			endVal = g.coerceToI64(endVal, g.exprType(call.Args[2]), out)
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_string_slice(ptr %s, i64 %s, i64 %s)\n",
				res, sVal, startVal, endVal)
			return res, checker.String, nil

		case "char_to_string":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			argVal = g.coerceToI64(argVal, g.exprType(call.Args[0]), out)
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_char_to_string(i64 %s)\n", res, argVal)
			return res, checker.String, nil

		case "read_file":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_read_file(ptr %s)\n", res, argVal)
			return res, checker.String, nil

		case "write_file":
			pathVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			contentVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			fmt.Fprintf(out, "  call void @jerry_write_file(ptr %s, ptr %s)\n", pathVal, contentVal)
			return "0", checker.Void, nil

		case "exit":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			// exit takes i32
			iReg := g.newTmp()
			fmt.Fprintf(out, "  %s = trunc i64 %s to i32\n", iReg, argVal)
			fmt.Fprintf(out, "  call void @exit(i32 %s)\n", iReg)
			g.terminated = true
			return "0", checker.Void, nil

		case "panic":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			fmt.Fprintf(out, "  call void @jerry_panic(ptr %s)\n", argVal)
			fmt.Fprintf(out, "  unreachable\n")
			g.terminated = true
			return "0", checker.Void, nil
		}

		// Is it a local variable with function type (closure stored in a variable)?
		if lvar, isLocal := g.locals[base.Ident]; isLocal && lvar.altType != nil && lvar.altType.Kind == checker.KindFunc {
			// calleeVal is already the closure ptr (loaded in genPrimary).
			// Fall through to the closure dispatch below by setting calleeTy.
			calleeTy = lvar.altType
			// (calleeVal already holds the loaded closure ptr)
			goto closureCall
		}

		// Named top-level function call.
		{
			fnName := base.Ident + "_jerry"
			var argLLVM []string
			for i, a := range call.Args {
				av, err := g.genExpr(a, out)
				if err != nil {
					return "", nil, err
				}
				at := g.exprType(a)
				if i < len(calleeTy.Params) {
					argLLVM = append(argLLVM, g.llvmType(calleeTy.Params[i])+" "+av)
				} else {
					argLLVM = append(argLLVM, g.llvmType(at)+" "+av)
				}
			}
			retTy := checker.Void
			if calleeTy != nil && calleeTy.Kind == checker.KindFunc && calleeTy.Return != nil {
				retTy = calleeTy.Return
			}
			retLLVM := g.llvmType(retTy)
			if retLLVM == "void" {
				fmt.Fprintf(out, "  call void @%s(%s)\n", fnName, strings.Join(argLLVM, ", "))
				return "0", checker.Void, nil
			}
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call %s @%s(%s)\n", res, retLLVM, fnName, strings.Join(argLLVM, ", "))
			return res, retTy, nil
		}
	}

closureCall:

	// Closure call: extract fn_ptr and env_ptr, call fn_ptr(env_ptr, args...).
	if calleeTy != nil && calleeTy.Kind == checker.KindFunc {
		fnPtrSlot := g.newTmp()
		fnPtr := g.newTmp()
		envSlot := g.newTmp()
		envPtr := g.newTmp()
		fmt.Fprintf(out, "  %s = getelementptr %%JerryClosure, ptr %s, i32 0, i32 0\n", fnPtrSlot, calleeVal)
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", fnPtr, fnPtrSlot)
		fmt.Fprintf(out, "  %s = getelementptr %%JerryClosure, ptr %s, i32 0, i32 1\n", envSlot, calleeVal)
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", envPtr, envSlot)

		// First arg is env_ptr, then the declared args.
		var argLLVM []string
		argLLVM = append(argLLVM, "ptr "+envPtr)
		for i, a := range call.Args {
			av, err := g.genExpr(a, out)
			if err != nil {
				return "", nil, err
			}
			if i < len(calleeTy.Params) {
				argLLVM = append(argLLVM, g.llvmType(calleeTy.Params[i])+" "+av)
			} else {
				at := g.exprType(a)
				argLLVM = append(argLLVM, g.llvmType(at)+" "+av)
			}
		}

		// Build function type signature for the indirect call.
		var paramTypes []string
		paramTypes = append(paramTypes, "ptr") // env
		for _, p := range calleeTy.Params {
			paramTypes = append(paramTypes, g.llvmType(p))
		}
		retTy := checker.Void
		if calleeTy.Return != nil {
			retTy = calleeTy.Return
		}
		retLLVM := g.llvmType(retTy)
		fnTySig := fmt.Sprintf("%s (%s)", retLLVM, strings.Join(paramTypes, ", "))

		if retLLVM == "void" {
			fmt.Fprintf(out, "  call void %s %s(%s)\n", fnTySig, fnPtr, strings.Join(argLLVM, ", "))
			return "0", checker.Void, nil
		}
		res := g.newTmp()
		fmt.Fprintf(out, "  %s = call %s %s(%s)\n", res, fnTySig, fnPtr, strings.Join(argLLVM, ", "))
		return res, retTy, nil
	}

	return "0", checker.Void, nil
}

func (g *Generator) emitPrint(val string, ty *checker.Type, noNewline bool, out *strings.Builder) {
	switch ty.Kind {
	case checker.KindInt:
		fmt.Fprintf(out, "  call void @jerry_print_int(i64 %s)\n", val)
	case checker.KindFloat:
		fmt.Fprintf(out, "  call void @jerry_print_float(double %s)\n", val)
	case checker.KindBool:
		ext := g.newTmp()
		fmt.Fprintf(out, "  %s = zext i1 %s to i8\n", ext, val)
		fmt.Fprintf(out, "  call void @jerry_print_bool(i8 %s)\n", ext)
	case checker.KindString:
		fmt.Fprintf(out, "  call void @jerry_print_string(ptr %s)\n", val)
	case checker.KindArray:
		fmt.Fprintf(out, "  call void @jerry_print_array(ptr %s)\n", val)
	default:
		fmt.Fprintf(out, "  call void @jerry_print_int(i64 0) ; unknown type\n")
	}
	if !noNewline {
		fmt.Fprintf(out, "  call void @jerry_println()\n")
	}
}

func (g *Generator) genPrimary(p *ast.PrimaryExpr, out *strings.Builder) (string, error) {
	switch {
	case p.Int != "":
		return p.Int, nil
	case p.Float != "":
		// LLVM requires decimal point in float literals
		if !strings.Contains(p.Float, ".") {
			return p.Float + ".0", nil
		}
		return p.Float, nil
	case p.Bool != "":
		if p.Bool == "true" {
			return "true", nil
		}
		return "false", nil
	case p.Null:
		return "null", nil
	case p.String != nil:
		return g.genStringLit(*p.String, out), nil
	case p.This:
		lvar, ok := g.locals["this"]
		if !ok {
			return "", fmt.Errorf("'this' not available")
		}
		res := g.newTmp()
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", res, lvar.reg)
		return res, nil
	case p.Ident != "":
		// Local variable?
		if lvar, ok := g.locals[p.Ident]; ok {
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = load %s, ptr %s\n", res, lvar.llvmTy, lvar.reg)
			return res, nil
		}
		// Named function reference (used as a value or about to be called).
		// Builtins and user-defined functions live at the LLVM function level;
		// we return a sentinel that genCall will use directly.
		// Return the identifier itself; genCall checks base.Ident for dispatch.
		return "%" + p.Ident + ".fnref", nil
	case p.Array != nil:
		return g.genArrayLit(p.Array, out)
	case p.FnExpr != nil:
		return g.genFnExpr(p.FnExpr, out)
	case p.NewExpr != nil:
		return g.genNewExpr(p.NewExpr, out)
	case p.Paren != nil:
		v, err := g.genExpr(p.Paren, out)
		return v, err
	}
	return "0", nil
}

func (g *Generator) genStringLit(s string, out *strings.Builder) string {
	id := len(g.strConsts)
	g.strConsts = append(g.strConsts, s)
	res := g.newTmp()
	fmt.Fprintf(out, "  %s = call ptr @jerry_string_new(ptr @.str.%d, i64 %d)\n",
		res, id, len(s))
	return res
}

func (g *Generator) genArrayLit(a *ast.ArrayLit, out *strings.Builder) (string, error) {
	var elemTy *checker.Type
	var elemLLVM string
	if len(a.Elems) > 0 {
		elemTy = g.exprType(a.Elems[0])
		elemLLVM = g.llvmType(elemTy)
	} else {
		elemTy = checker.Int
		elemLLVM = "i64"
	}
	elemSize := g.typeSize(elemTy)
	arrReg := g.newTmp()
	fmt.Fprintf(out, "  %s = call ptr @jerry_array_new(i64 %d, i64 %d)\n",
		arrReg, elemSize, len(a.Elems))
	for _, e := range a.Elems {
		ev, err := g.genExpr(e, out)
		if err != nil {
			return "", err
		}
		slot := g.newTmp() + ".slot"
		fmt.Fprintf(out, "  %s = alloca %s\n", slot, elemLLVM)
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", elemLLVM, ev, slot)
		fmt.Fprintf(out, "  call void @jerry_array_push(ptr %s, ptr %s)\n", arrReg, slot)
	}
	return arrReg, nil
}

func (g *Generator) genFnExpr(fn *ast.FnExpr, out *strings.Builder) (string, error) {
	// Generate an anonymous function and return a pointer to it.
	// Closure capture is not yet implemented — free variables will cause errors.
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

func (g *Generator) genNewExpr(n *ast.NewExpr, out *strings.Builder) (string, error) {
	ci := g.info.Classes[n.ClassName]
	if ci == nil {
		return "", fmt.Errorf("unknown class %s", n.ClassName)
	}

	// Allocate the struct.
	sizeReg := g.newTmp()
	objReg := g.newTmp()
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizeReg, n.ClassName)
	sizeInt := g.newTmp()
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", sizeInt, sizeReg)
	fmt.Fprintf(out, "  %s = call ptr @jerry_alloc(i64 %s)\n", objReg, sizeInt)

	// Set vtable pointer.
	vtableSlot := g.newTmp()
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr %s, i32 0, i32 0\n",
		vtableSlot, n.ClassName, objReg)
	if len(ci.MethodOrder) > 0 {
		fmt.Fprintf(out, "  store ptr @vtable.%s, ptr %s\n", n.ClassName, vtableSlot)
	} else {
		fmt.Fprintf(out, "  store ptr null, ptr %s\n", vtableSlot)
	}

	// Call constructor method "new" if present.
	if _, hasNew := ci.Methods["new"]; hasNew {
		var argLLVM []string
		argLLVM = append(argLLVM, "ptr "+objReg)
		mt := ci.Methods["new"]
		for i, a := range n.Args {
			av, err := g.genExpr(a, out)
			if err != nil {
				return "", err
			}
			if i < len(mt.Params) {
				argLLVM = append(argLLVM, g.llvmType(mt.Params[i])+" "+av)
			}
		}
		fnName := g.methodFnName(n.ClassName, "new")
		fmt.Fprintf(out, "  call void @%s(%s)\n", fnName, strings.Join(argLLVM, ", "))
	}

	return objReg, nil
}

// ── Method call on object ─────────────────────────────────────────────────────

// genMethodCall generates code for obj.method(args...).
// className is the static class name of obj.
func (g *Generator) genMethodCall(
	className, method string,
	objVal string,
	args []*ast.Expr,
	out *strings.Builder,
) (string, *checker.Type, error) {
	ci := g.info.Classes[className]
	mt := ci.Methods[method]
	if mt == nil {
		return "", nil, fmt.Errorf("class %s has no method %q", className, method)
	}

	var argLLVM []string
	argLLVM = append(argLLVM, "ptr "+objVal)
	for i, a := range args {
		av, err := g.genExpr(a, out)
		if err != nil {
			return "", nil, err
		}
		if i < len(mt.Params) {
			argLLVM = append(argLLVM, g.llvmType(mt.Params[i])+" "+av)
		} else {
			at := g.exprType(a)
			argLLVM = append(argLLVM, g.llvmType(at)+" "+av)
		}
	}

	fnName := g.methodFnName(className, method)
	retLLVM := g.llvmType(mt.Return)
	if retLLVM == "void" {
		fmt.Fprintf(out, "  call void @%s(%s)\n", fnName, strings.Join(argLLVM, ", "))
		return "0", checker.Void, nil
	}
	res := g.newTmp()
	fmt.Fprintf(out, "  %s = call %s @%s(%s)\n", res, retLLVM, fnName, strings.Join(argLLVM, ", "))
	return res, mt.Return, nil
}

// ── Type helpers ──────────────────────────────────────────────────────────────

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
	switch te.Name {
	case "void":
		return checker.Void
	case "int":
		return checker.Int
	case "float":
		return checker.Float
	case "bool":
		return checker.Bool
	case "string":
		return checker.String
	default:
		return checker.ClassType(te.Name)
	}
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

// ── Utility ───────────────────────────────────────────────────────────────────

func (g *Generator) newTmp() string {
	g.tmp++
	return fmt.Sprintf("%%t%d", g.tmp)
}

// coerceToI64 emits a zext if val is i1 (bool) and returns the i64 register.
func (g *Generator) coerceToI64(val string, ty *checker.Type, out *strings.Builder) string {
	if ty != nil && ty.Kind == checker.KindBool {
		res := g.newTmp()
		fmt.Fprintf(out, "  %s = zext i1 %s to i64\n", res, val)
		return res
	}
	return val
}

func (g *Generator) newLabel(prefix string) string {
	g.lbl++
	return fmt.Sprintf("%s.%d", prefix, g.lbl)
}

// emitBlockLabel emits a basic block label, updates curBlock, and resets terminated.
func (g *Generator) emitBlockLabel(name string, out *strings.Builder) {
	fmt.Fprintf(out, "%s:\n", name)
	g.curBlock = name
	g.terminated = false
}

func (g *Generator) allocaInto(out *strings.Builder, name, llvmTy string) string {
	g.tmp++
	reg := fmt.Sprintf("%%%s.%d", name, g.tmp)
	fmt.Fprintf(out, "  %s = alloca %s\n", reg, llvmTy)
	return reg
}

func (g *Generator) storeInto(out *strings.Builder, llvmTy, val, reg string) {
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", llvmTy, val, reg)
}

// saveContext / restoreContext handles nested function generation.
type genContext struct {
	curFnName  string
	retType    *checker.Type
	locals     map[string]*localVar
	labelStack []loopLabels
	terminated bool
	tmp        int
	lbl        int
}

func (g *Generator) saveContext() genContext {
	return genContext{
		curFnName:  g.curFnName,
		retType:    g.retType,
		locals:     g.locals,
		labelStack: g.labelStack,
		terminated: g.terminated,
		tmp:        g.tmp,
		lbl:        g.lbl,
	}
}

func (g *Generator) restoreContext(ctx genContext) {
	g.curFnName = ctx.curFnName
	g.retType = ctx.retType
	g.locals = ctx.locals
	g.labelStack = ctx.labelStack
	g.terminated = ctx.terminated
	// Don't restore tmp/lbl — keep them globally unique.
}

func (g *Generator) cloneLocals() map[string]*localVar {
	m := make(map[string]*localVar, len(g.locals))
	for k, v := range g.locals {
		m[k] = v
	}
	return m
}

// builtinReturnType maps builtin function names to their return types.
// Used by the codegen's type-inference helpers (primaryType etc.) which
// don't have access to the checker's scope.
var builtinReturnType = map[string]*checker.Type{
	"print":            checker.Void,
	"write":            checker.Void,
	"println":          checker.Void,
	"len":              checker.Int,
	"push":             checker.Void,
	"int_to_string":    checker.String,
	"float_to_string":  checker.String,
	"char_at":          checker.Int,
	"string_slice":     checker.String,
	"char_to_string":   checker.String,
	"read_file":        checker.String,
	"write_file":       checker.Void,
	"exit":             checker.Void,
	"panic":            checker.Void,
}

// llvmEscapeString escapes a Go string for LLVM IR constant syntax.
func llvmEscapeString(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '\\':
			sb.WriteString("\\\\")
		case '"':
			sb.WriteString("\\22")
		case '\n':
			sb.WriteString("\\0A")
		case '\r':
			sb.WriteString("\\0D")
		case '\t':
			sb.WriteString("\\09")
		default:
			if b < 0x20 || b >= 0x7f {
				sb.WriteString(fmt.Sprintf("\\%02X", b))
			} else {
				sb.WriteByte(b)
			}
		}
	}
	return sb.String()
}

// Suppress unused import
var _ = strconv.Itoa
