package codegen

import (
	"fmt"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
)

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
			if gvar, ok2 := g.globals[prim.Ident]; ok2 {
				if isHeapType(gvar.altType) {
					oldReg := g.newTmp()
					fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", oldReg, gvar.reg)
					fmt.Fprintf(out, "  call void @jerry_retain(ptr %s)\n", rhs)
					g.storeInto(out, lt, rhs, gvar.reg)
					fmt.Fprintf(out, "  call void @jerry_release(ptr %s)\n", oldReg)
				} else {
					g.storeInto(out, lt, rhs, gvar.reg)
				}
				return nil
			}
			return fmt.Errorf("undefined variable %q", prim.Ident)
		}
		// For heap-type locals, retain the new value and release the displaced one
		// so the scope-exit release stays balanced regardless of how many times the
		// variable has been reassigned.
		if isHeapType(lvar.altType) {
			oldReg := g.newTmp()
			fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", oldReg, lvar.reg)
			fmt.Fprintf(out, "  call void @jerry_retain(ptr %s)\n", rhs)
			g.storeInto(out, lt, rhs, lvar.reg)
			fmt.Fprintf(out, "  call void @jerry_release(ptr %s)\n", oldReg)
		} else {
			g.storeInto(out, lt, rhs, lvar.reg)
		}
		return nil
	}
	// Postfix: could be obj.field or arr[idx] — build the base then store.
	lastOp := post.Ops[len(post.Ops)-1]
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
		// For heap-type fields, retain the incoming value and release the
		// displaced one.  jerry_retain/release are both null-safe, so this
		// is correct even during construction (fields start zero-initialised).
		if fieldTy := ci.Fields[lastOp.Field]; isHeapType(fieldTy) {
			oldReg := g.newTmp()
			fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", oldReg, gepReg)
			fmt.Fprintf(out, "  call void @jerry_retain(ptr %s)\n", rhs)
			fmt.Fprintf(out, "  store ptr %s, ptr %s\n", rhs, gepReg)
			fmt.Fprintf(out, "  call void @jerry_release(ptr %s)\n", oldReg)
		} else {
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", lt, rhs, gepReg)
		}
	case lastOp.Index != nil:
		idxVal, err := g.genExpr(lastOp.Index, out)
		if err != nil {
			return err
		}
		if baseTy.Kind == checker.KindArray {
			tmpAlloca := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", tmpAlloca, lt)
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", lt, rhs, tmpAlloca)
			fmt.Fprintf(out, "  call void @jerry_array_set(ptr %s, i64 %s, ptr %s)\n",
				baseVal, idxVal, tmpAlloca)
		} else if baseTy.Kind == checker.KindMap {
			keyTy := baseTy.Key
			keySlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), idxVal, keySlot)
			valSlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", valSlot, lt)
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", lt, rhs, valSlot)
			fmt.Fprintf(out, "  call void @jerry_map_set(ptr %s, ptr %s, ptr %s)\n",
				baseVal, keySlot, valSlot)
		} else {
			return fmt.Errorf("index assignment on non-array/non-map type %s", baseTy)
		}
	default:
		return fmt.Errorf("invalid assignment target")
	}
	return nil
}

func (g *Generator) genOr(a *ast.OrExpr, out *strings.Builder) (string, error) {
	val, err := g.genAnd(a.Left, out)
	if err != nil {
		return "", err
	}
	for _, r := range a.Rest {
		lhsBlock := g.curBlock
		trueLabel := g.newLabel("or.true")
		rightLabel := g.newLabel("or.rhs")
		mergeLabel := g.newLabel("or.merge")
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", val, trueLabel, rightLabel)
		g.emitBlockLabel(rightLabel, out)
		rval, err := g.genAnd(r.Right, out)
		if err != nil {
			return "", err
		}
		rhsEndBlock := g.curBlock
		fmt.Fprintf(out, "  br label %%%s\n", mergeLabel)
		g.emitBlockLabel(trueLabel, out)
		fmt.Fprintf(out, "  br label %%%s\n", mergeLabel)
		g.emitBlockLabel(mergeLabel, out)
		res := g.newTmp()
		_ = lhsBlock
		fmt.Fprintf(out, "  %s = phi i1 [ true, %%%s ], [ %s, %%%s ]\n", res, trueLabel, rval, rhsEndBlock)
		val = res
	}
	return val, nil
}

func (g *Generator) genAnd(a *ast.AndExpr, out *strings.Builder) (string, error) {
	val, err := g.genEq(a.Left, out)
	if err != nil {
		return "", err
	}
	for _, r := range a.Rest {
		falseLabel := g.newLabel("and.false")
		rightLabel := g.newLabel("and.rhs")
		mergeLabel := g.newLabel("and.merge")
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", val, rightLabel, falseLabel)
		g.emitBlockLabel(rightLabel, out)
		rval, err := g.genEq(r.Right, out)
		if err != nil {
			return "", err
		}
		rhsEndBlock := g.curBlock
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
				return "", nil, fmt.Errorf("field access on non-class type %s (field: %s, pos: %v)", ty, op.Field, op.Pos)
			}
			ci := g.info.Classes[ty.ClassName]
			if _, isMethod := ci.Methods[op.Field]; isMethod {
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
			idxVal, err := g.genExpr(op.Index, out)
			if err != nil {
				return "", nil, err
			}
			if ty.Kind == checker.KindArray {
				ptrReg := g.newTmp()
				fmt.Fprintf(out, "  %s = call ptr @jerry_array_get(ptr %s, i64 %s)\n",
					ptrReg, val, idxVal)
				loadReg := g.newTmp()
				elemTy := ty.Elem
				fmt.Fprintf(out, "  %s = load %s, ptr %s\n",
					loadReg, g.llvmType(elemTy), ptrReg)
				val = loadReg
				ty = elemTy
			} else if ty.Kind == checker.KindMap {
				keyTy := ty.Key
				keySlot := g.newTmp() + ".slot"
				fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
				fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), idxVal, keySlot)
				ptrReg := g.newTmp()
				fmt.Fprintf(out, "  %s = call ptr @jerry_map_get(ptr %s, ptr %s)\n",
					ptrReg, val, keySlot)
				valTy := ty.Value
				loadReg := g.newTmp()
				fmt.Fprintf(out, "  %s = load %s, ptr %s\n",
					loadReg, g.llvmType(valTy), ptrReg)
				val = loadReg
				ty = valTy
			} else {
				return "", nil, fmt.Errorf("index on non-array/non-map type %s", ty)
			}

		case op.PlusPlus, op.MinusMinus:
			varName := p.Base.Ident
			lvar, ok := g.locals[varName]
			if !ok {
				lvar, ok = g.globals[varName]
			}
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
			g.releaseStrLitArgs(call.Args, []string{argVal}, out)
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
			g.releaseStrLitArgs(call.Args, []string{pathVal, contentVal}, out)
			return "0", checker.Void, nil

		case "exit":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			fmt.Fprintf(out, "  call void @jerry_exit(i64 %s)\n", argVal)
			fmt.Fprintf(out, "  unreachable\n")
			g.terminated = true
			return "0", checker.Void, nil

		case "args":
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_args()\n", res)
			return res, checker.ArrayOf(checker.String), nil

		case "print_err":
			if len(call.Args) != 1 {
				return "", nil, fmt.Errorf("print_err() takes 1 argument")
			}
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			fmt.Fprintf(out, "  call void @jerry_print_err(ptr %s)\n", argVal)
			g.releaseStrLitArgs(call.Args, []string{argVal}, out)
			return "0", checker.Void, nil

		case "read_stdin":
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_read_stdin()\n", res)
			return res, checker.String, nil

		case "now_millis":
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call i64 @jerry_now_millis()\n", res)
			return res, checker.Int, nil

		case "now_seconds":
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call i64 @jerry_now_seconds()\n", res)
			return res, checker.Int, nil

		case "now_string":
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_now_string()\n", res)
			return res, checker.String, nil

		case "each_line":
			if len(call.Args) != 2 {
				return "", nil, fmt.Errorf("each_line() takes 2 arguments (filename, callback)")
			}
			pathVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			callbackVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			fmt.Fprintf(out, "  call void @jerry_each_line(ptr %s, ptr %s)\n", pathVal, callbackVal)
			return "0", checker.Void, nil

		case "panic":
			argVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			// Note: no release needed here — jerry_panic never returns.
			fmt.Fprintf(out, "  call void @jerry_panic(ptr %s)\n", argVal)
			fmt.Fprintf(out, "  unreachable\n")
			g.terminated = true
			return "0", checker.Void, nil

		case "map_set":
			mapVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			mapTy := g.exprType(call.Args[0])
			keyVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			valVal, err := g.genExpr(call.Args[2], out)
			if err != nil {
				return "", nil, err
			}
			keyTy := checker.String
			valTy := checker.Int
			if mapTy.Kind == checker.KindMap {
				keyTy = mapTy.Key
				valTy = mapTy.Value
			}
			keySlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), keyVal, keySlot)
			valSlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", valSlot, g.llvmType(valTy))
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(valTy), valVal, valSlot)
			fmt.Fprintf(out, "  call void @jerry_map_set(ptr %s, ptr %s, ptr %s)\n",
				mapVal, keySlot, valSlot)
			return "0", checker.Void, nil

		case "map_get":
			mapVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			mapTy := g.exprType(call.Args[0])
			keyVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			keyTy := checker.String
			retTy := checker.Void
			if mapTy.Kind == checker.KindMap {
				keyTy = mapTy.Key
				retTy = mapTy.Value
			}
			keySlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), keyVal, keySlot)
			vptr := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_map_get(ptr %s, ptr %s)\n",
				vptr, mapVal, keySlot)
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = load %s, ptr %s\n", res, g.llvmType(retTy), vptr)
			return res, retTy, nil

		case "map_has":
			mapVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			mapTy := g.exprType(call.Args[0])
			keyVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			keyTy := checker.String
			if mapTy.Kind == checker.KindMap {
				keyTy = mapTy.Key
			}
			keySlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), keyVal, keySlot)
			raw := g.newTmp()
			fmt.Fprintf(out, "  %s = call i8 @jerry_map_has(ptr %s, ptr %s)\n",
				raw, mapVal, keySlot)
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = icmp ne i8 %s, 0\n", res, raw)
			return res, checker.Bool, nil

		case "map_delete":
			mapVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			mapTy := g.exprType(call.Args[0])
			keyVal, err := g.genExpr(call.Args[1], out)
			if err != nil {
				return "", nil, err
			}
			keyTy := checker.String
			if mapTy.Kind == checker.KindMap {
				keyTy = mapTy.Key
			}
			keySlot := g.newTmp() + ".slot"
			fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
			fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), keyVal, keySlot)
			fmt.Fprintf(out, "  call void @jerry_map_delete(ptr %s, ptr %s)\n",
				mapVal, keySlot)
			return "0", checker.Void, nil

		case "map_len":
			mapVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call i64 @jerry_map_len(ptr %s)\n", res, mapVal)
			return res, checker.Int, nil

		case "map_keys":
			mapVal, err := g.genExpr(call.Args[0], out)
			if err != nil {
				return "", nil, err
			}
			mapTy := g.exprType(call.Args[0])
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call ptr @jerry_map_keys(ptr %s)\n", res, mapVal)
			retTy := checker.ArrayOf(checker.String)
			if mapTy.Kind == checker.KindMap {
				retTy = checker.ArrayOf(mapTy.Key)
			}
			return res, retTy, nil
		}

		// Is it a local variable with function type (closure stored in a variable)?
		if lvar, isLocal := g.locals[base.Ident]; isLocal && lvar.altType != nil && lvar.altType.Kind == checker.KindFunc {
			calleeTy = lvar.altType
			goto closureCall
		}

		// Named top-level function call.
		{
			fnName := base.Ident + "_jerry"
			var argVals []string
			var argLLVM []string
			for i, a := range call.Args {
				av, err := g.genExpr(a, out)
				if err != nil {
					return "", nil, err
				}
				argVals = append(argVals, av)
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
				g.releaseStrLitArgs(call.Args, argVals, out)
				return "0", checker.Void, nil
			}
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = call %s @%s(%s)\n", res, retLLVM, fnName, strings.Join(argLLVM, ", "))
			g.releaseStrLitArgs(call.Args, argVals, out)
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

		var argVals []string
		var argLLVM []string
		argLLVM = append(argLLVM, "ptr "+envPtr)
		for i, a := range call.Args {
			av, err := g.genExpr(a, out)
			if err != nil {
				return "", nil, err
			}
			argVals = append(argVals, av)
			if i < len(calleeTy.Params) {
				argLLVM = append(argLLVM, g.llvmType(calleeTy.Params[i])+" "+av)
			} else {
				at := g.exprType(a)
				argLLVM = append(argLLVM, g.llvmType(at)+" "+av)
			}
		}

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
			fmt.Fprintf(out, "  call %s %s(%s)\n", fnTySig, fnPtr, strings.Join(argLLVM, ", "))
			g.releaseStrLitArgs(call.Args, argVals, out)
			return "0", checker.Void, nil
		}
		res := g.newTmp()
		fmt.Fprintf(out, "  %s = call %s %s(%s)\n", res, fnTySig, fnPtr, strings.Join(argLLVM, ", "))
		g.releaseStrLitArgs(call.Args, argVals, out)
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
		if lvar, ok := g.locals[p.Ident]; ok {
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = load %s, ptr %s\n", res, lvar.llvmTy, lvar.reg)
			return res, nil
		}
		if gvar, ok := g.globals[p.Ident]; ok {
			res := g.newTmp()
			fmt.Fprintf(out, "  %s = load %s, ptr %s\n", res, gvar.llvmTy, gvar.reg)
			return res, nil
		}
		return "%" + p.Ident + ".fnref", nil
	case p.MapLit != nil:
		return g.genMapLit(p.MapLit, out)
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

// releaseStrLitArgs emits a jerry_release for each argument in args whose
// expression was a bare string literal. Call this immediately after emitting
// a function call so that string literals used as arguments are freed once the
// callee is done with them.
func (g *Generator) releaseStrLitArgs(args []*ast.Expr, argVals []string, out *strings.Builder) {
	for i, a := range args {
		if i < len(argVals) && bareStringLit(a) != nil {
			fmt.Fprintf(out, "  call void @jerry_release(ptr %s)\n", argVals[i])
		}
	}
}

func (g *Generator) genMapLit(m *ast.MapLit, out *strings.Builder) (string, error) {
	// Determine key and value types.
	var keyTy, valTy *checker.Type
	if len(m.Entries) > 0 {
		keyTy = g.exprType(m.Entries[0].Key)
		valTy = g.exprType(m.Entries[0].Value)
	} else if rt, ok := m.ResolvedType.(*checker.Type); ok && rt != nil && rt.Kind == checker.KindMap {
		keyTy = rt.Key
		valTy = rt.Value
	} else {
		// Empty map without annotation context — default to string/int (safe fallback).
		keyTy = checker.String
		valTy = checker.Int
	}

	stringKeys := int8(0)
	if keyTy.Kind == checker.KindString {
		stringKeys = 1
	}
	valSize := g.typeSize(valTy)

	mapReg := g.newTmp()
	fmt.Fprintf(out, "  %s = call ptr @jerry_map_new(i8 %d, i64 %d)\n",
		mapReg, stringKeys, valSize)

	for _, e := range m.Entries {
		kv, err := g.genExpr(e.Key, out)
		if err != nil {
			return "", err
		}
		vv, err := g.genExpr(e.Value, out)
		if err != nil {
			return "", err
		}
		keySlot := g.newTmp() + ".slot"
		valSlot := g.newTmp() + ".slot"
		fmt.Fprintf(out, "  %s = alloca %s\n", keySlot, g.llvmType(keyTy))
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(keyTy), kv, keySlot)
		fmt.Fprintf(out, "  %s = alloca %s\n", valSlot, g.llvmType(valTy))
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", g.llvmType(valTy), vv, valSlot)
		fmt.Fprintf(out, "  call void @jerry_map_set(ptr %s, ptr %s, ptr %s)\n",
			mapReg, keySlot, valSlot)
	}
	return mapReg, nil
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
	heapElems := int8(0)
	if isHeapType(elemTy) {
		heapElems = 1
	}
	arrReg := g.newTmp()
	fmt.Fprintf(out, "  %s = call ptr @jerry_array_new(i64 %d, i64 %d, i8 %d)\n",
		arrReg, elemSize, len(a.Elems), heapElems)
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
