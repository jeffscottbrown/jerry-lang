package codegen

import (
	"fmt"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
)

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

// genMethodCall generates code for obj.method(args...).
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

// genNewExpr generates code for `new ClassName(args...)`.
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
