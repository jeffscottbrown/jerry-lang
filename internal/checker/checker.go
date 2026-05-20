package checker

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/alecthomas/participle/v2/lexer"
	"github.com/jeffscottbrown/jerry-lang/internal/ast"
)

// Checker performs type checking and semantic analysis on an Jerry AST.
type Checker struct {
	info   *Info
	scope  *Scope // current scope
	retTy  *Type  // return type of current function
	inLoop bool
	errors []CheckError
}

func New() *Checker {
	c := &Checker{info: NewInfo()}
	c.scope = newBuiltinScope()
	return c
}

// Check type-checks a program and returns type info plus any errors.
func Check(prog *ast.Program) (*Info, []CheckError) {
	c := New()

	// First pass: register all top-level declarations (functions and classes)
	// so forward references work.
	for _, tl := range prog.Stmts {
		if tl.FnDecl != nil {
			c.registerFn(tl.FnDecl)
		}
		if tl.Class != nil {
			c.registerClass(tl.Class)
		}
	}

	// Second pass: check bodies.
	for _, tl := range prog.Stmts {
		switch {
		case tl.FnDecl != nil:
			c.checkFnDecl(tl.FnDecl)
		case tl.Class != nil:
			c.checkClassDecl(tl.Class)
		case tl.VarDecl != nil:
			c.checkVarDecl(tl.VarDecl)
		}
	}

	return c.info, c.errors
}

// CheckAll is the multi-file compilation entry point.
//
//   - projectProgs: one *ast.Program per project source file (may contain IncludeDecls)
//   - coreAST:      parsed stdlib/core.jer, always in scope for every file
//   - stdlibASTs:   map from stdlib module name → parsed AST, for explicit @includes
//   - remoteASTs:   map from import path → slice of ASTs (one per .jer file in module)
//
// Per-file scoping rules:
//   - Compiler primitives (print, len, etc.) are always in scope.
//   - core.jer functions are always in scope.
//   - All project-defined functions/classes are always in scope for each other.
//   - Functions from a stdlib/remote module are only in scope for files that
//     explicitly include it.
func CheckAll(
	projectProgs []*ast.Program,
	coreAST *ast.Program,
	stdlibASTs map[string]*ast.Program,
	remoteASTs map[string][]*ast.Program,
) (*Info, []CheckError) {
	info := NewInfo()

	// ── Build scope layers ────────────────────────────────────────────────────

	builtinScope := newBuiltinScope()

	// core scope: child of builtins; core.jer symbols land here.
	coreChecker := &Checker{info: info, scope: NewScope(builtinScope)}
	if coreAST != nil {
		for _, tl := range coreAST.Stmts {
			if tl.FnDecl != nil {
				coreChecker.registerFn(tl.FnDecl)
			}
			if tl.Class != nil {
				coreChecker.registerClass(tl.Class)
			}
		}
	}
	coreScope := coreChecker.scope

	// project scope: child of core; all project-defined names land here.
	projectChecker := &Checker{info: info, scope: NewScope(coreScope)}
	for _, prog := range projectProgs {
		for _, tl := range prog.Stmts {
			if tl.FnDecl != nil {
				projectChecker.registerFn(tl.FnDecl)
			}
			if tl.Class != nil {
				projectChecker.registerClass(tl.Class)
			}
		}
	}
	// Pre-check all project-file global variable declarations into projectScope
	// so they are visible across all project files (like functions and classes are).
	for _, prog := range projectProgs {
		for _, tl := range prog.Stmts {
			if tl.VarDecl != nil {
				projectChecker.checkVarDecl(tl.VarDecl)
			}
		}
	}
	projectScope := projectChecker.scope

	var allErrors []CheckError
	allErrors = append(allErrors, projectChecker.errors...)

	// ── Check core.jer bodies ─────────────────────────────────────────────────
	if coreAST != nil {
		for _, tl := range coreAST.Stmts {
			switch {
			case tl.FnDecl != nil:
				coreChecker.checkFnDecl(tl.FnDecl)
			case tl.Class != nil:
				coreChecker.checkClassDecl(tl.Class)
			case tl.VarDecl != nil:
				coreChecker.checkVarDecl(tl.VarDecl)
			}
		}
		for _, ce := range coreChecker.errors {
			allErrors = append(allErrors, CheckError{Msg: "stdlib/core: " + ce.Msg, Pos: ce.Pos})
		}
	}

	// ── Check each required stdlib module (once) ──────────────────────────────
	// checkedStdlib maps stdlib module name to the checker scope produced when
	// that module was checked. Storing the scope lets project-file checkers
	// import global-variable symbols (not just functions and classes).
	checkedStdlib := map[string]*Scope{}
	for _, prog := range projectProgs {
		for _, tl := range prog.Stmts {
			if tl.Include == nil || tl.Include.Stdlib == "" {
				continue
			}
			name := tl.Include.Stdlib
			if _, already := checkedStdlib[name]; already {
				continue
			}
			stdAST, ok := stdlibASTs[name]
			if !ok {
				// Error reported during include resolution in main; skip body check.
				continue
			}
			// Stdlib sees: core scope + its own forward-declared names.
			sc := &Checker{info: info, scope: NewScope(coreScope)}
			for _, stl := range stdAST.Stmts {
				if stl.FnDecl != nil {
					sc.registerFn(stl.FnDecl)
				}
				if stl.Class != nil {
					sc.registerClass(stl.Class)
				}
			}
			for _, stl := range stdAST.Stmts {
				switch {
				case stl.FnDecl != nil:
					sc.checkFnDecl(stl.FnDecl)
				case stl.Class != nil:
					sc.checkClassDecl(stl.Class)
				case stl.VarDecl != nil:
					sc.checkVarDecl(stl.VarDecl)
				}
			}
			checkedStdlib[name] = sc.scope
			for _, ce := range sc.errors {
				allErrors = append(allErrors, CheckError{Msg: fmt.Sprintf("stdlib/%s: %s", name, ce.Msg), Pos: ce.Pos})
			}
		}
	}

	// ── Check each required remote module (once) ──────────────────────────────
	// checkedRemote maps a remote import path to the checker scope produced when
	// that module was checked. Storing the scope lets project-file checkers
	// import module-level `let` symbols (not just functions and classes).
	checkedRemote := map[string]*Scope{}
	for _, prog := range projectProgs {
		for _, tl := range prog.Stmts {
			if tl.Include == nil || tl.Include.Remote == "" {
				continue
			}
			path := tl.Include.Remote
			if _, already := checkedRemote[path]; already {
				continue
			}
			progs, ok := remoteASTs[path]
			if !ok {
				continue
			}
			// Remote module sees: core scope + its own forward-declared names.
			rc := &Checker{info: info, scope: NewScope(coreScope)}
			for _, p := range progs {
				for _, stl := range p.Stmts {
					if stl.FnDecl != nil {
						rc.registerFn(stl.FnDecl)
					}
					if stl.Class != nil {
						rc.registerClass(stl.Class)
					}
				}
			}
			for _, p := range progs {
				for _, stl := range p.Stmts {
					switch {
					case stl.FnDecl != nil:
						rc.checkFnDecl(stl.FnDecl)
					case stl.Class != nil:
						rc.checkClassDecl(stl.Class)
					case stl.VarDecl != nil:
						rc.checkVarDecl(stl.VarDecl)
					}
				}
			}
			checkedRemote[path] = rc.scope
			for _, ce := range rc.errors {
				allErrors = append(allErrors, CheckError{Msg: fmt.Sprintf("%s: %s", path, ce.Msg), Pos: ce.Pos})
			}
		}
	}

	// ── Check each project file with its own per-file scope ───────────────────
	for _, prog := range projectProgs {
		// File scope is a child of the project scope.
		fc := &Checker{info: info, scope: NewScope(projectScope)}

		// Bring explicitly included stdlib and remote symbols into this file's scope.
		for _, tl := range prog.Stmts {
			if tl.Include == nil {
				continue
			}
			if tl.Include.Stdlib != "" {
				stdAST, ok := stdlibASTs[tl.Include.Stdlib]
				if !ok {
					fc.errors = append(fc.errors, CheckError{
						Msg: fmt.Sprintf("unknown stdlib module @%s", tl.Include.Stdlib),
						Pos: tl.Include.Pos,
					})
					continue
				}
				for _, stl := range stdAST.Stmts {
					if stl.FnDecl != nil {
						fc.registerFn(stl.FnDecl)
					}
					if stl.Class != nil {
						fc.registerClass(stl.Class)
					}
					if stl.VarDecl != nil {
						if stdScope, ok := checkedStdlib[tl.Include.Stdlib]; ok {
							if sym, found := stdScope.Lookup(stl.VarDecl.Name); found {
								_ = fc.scope.Define(sym)
							}
						}
					}
				}
			} else if tl.Include.Remote != "" {
				progs, ok := remoteASTs[tl.Include.Remote]
				if !ok {
					fc.errors = append(fc.errors, CheckError{
						Msg: fmt.Sprintf("unknown remote module %q", tl.Include.Remote),
						Pos: tl.Include.Pos,
					})
					continue
				}
				for _, p := range progs {
					for _, stl := range p.Stmts {
						if stl.FnDecl != nil {
							fc.registerFn(stl.FnDecl)
						}
						if stl.Class != nil {
							fc.registerClass(stl.Class)
						}
						if stl.VarDecl != nil {
							if remoteScope, ok := checkedRemote[tl.Include.Remote]; ok {
								if sym, found := remoteScope.Lookup(stl.VarDecl.Name); found {
									_ = fc.scope.Define(sym)
								}
							}
						}
					}
				}
			}
		}

		// Check this file's top-level declaration bodies.
		for _, tl := range prog.Stmts {
			switch {
			case tl.FnDecl != nil:
				fc.checkFnDecl(tl.FnDecl)
			case tl.Class != nil:
				fc.checkClassDecl(tl.Class)
			// tl.VarDecl: already pre-checked into projectScope; skip.
			// tl.Include: no body to check.
			}
		}
		allErrors = append(allErrors, fc.errors...)
	}

	return info, allErrors
}

func (c *Checker) errorf(pos lexer.Position, format string, args ...any) {
	c.errors = append(c.errors, CheckError{
		Msg: fmt.Sprintf(format, args...),
		Pos: pos,
	})
}

func (c *Checker) setType(expr *ast.Expr, t *Type) {
	key := uintptr(unsafe.Pointer(expr))
	c.info.ExprType[key] = t
	expr.ResolvedType = t
}

func (c *Checker) typeOf(expr *ast.Expr) *Type {
	if expr == nil {
		return Void
	}
	if t, ok := expr.ResolvedType.(*Type); ok {
		return t
	}
	return Void
}

// ── Type resolution ──────────────────────────────────────────────────────────

func (c *Checker) resolveTypeExpr(te *ast.TypeExpr) *Type {
	if te == nil {
		return Void
	}
	// Map type: map<K, V>
	if te.MapType != nil {
		keyTy := c.resolveTypeExpr(te.MapType.Key)
		valTy := c.resolveTypeExpr(te.MapType.Value)
		return MapOf(keyTy, valTy)
	}
	// Function type: fn(T, T): T
	if te.FnType != nil {
		var params []*Type
		for _, p := range te.FnType.Params {
			params = append(params, c.resolveTypeExpr(p))
		}
		ret := c.resolveTypeExpr(te.FnType.Return)
		return FuncType(params, ret)
	}
	var base *Type
	switch te.Name {
	case "void":
		base = Void
	case "int":
		base = Int
	case "float":
		base = Float
	case "bool":
		base = Bool
	case "string":
		base = String
	default:
		if _, ok := c.info.Classes[te.Name]; ok {
			base = ClassType(te.Name)
		} else {
			c.errorf(te.Pos, "unknown type %q", te.Name)
			base = Void
		}
	}
	if te.Array() {
		base = ArrayOf(base)
	}
	return base
}

// ── Top-level registration (first pass) ─────────────────────────────────────

func (c *Checker) registerFn(fn *ast.FnDecl) {
	var params []*Type
	for _, p := range fn.Params {
		params = append(params, c.resolveTypeExpr(p.Type))
	}
	ret := c.resolveTypeExpr(fn.Ret)
	ft := FuncType(params, ret)
	c.info.Funcs[fn.Name] = ft
	c.scope.Define(&Symbol{Name: fn.Name, Kind: SymFunc, Type: ft})
}

func (c *Checker) registerClass(cl *ast.ClassDecl) {
	// Register the class name first so self-referencing types resolve correctly.
	ci := NewClassInfo(cl.Name)
	c.info.Classes[cl.Name] = ci
	c.scope.Define(&Symbol{Name: cl.Name, Kind: SymClass,
		Type: ClassType(cl.Name)})

	for _, m := range cl.Members {
		if m.Field != nil {
			ft := c.resolveTypeExpr(m.Field.Type)
			ci.Fields[m.Field.Name] = ft
			ci.FieldOrder = append(ci.FieldOrder, m.Field.Name)
		}
	}
	for _, m := range cl.Members {
		if m.Method != nil {
			var params []*Type
			for _, p := range m.Method.Params {
				params = append(params, c.resolveTypeExpr(p.Type))
			}
			ret := c.resolveTypeExpr(m.Method.Ret)
			mt := FuncType(params, ret)
			ci.Methods[m.Method.Name] = mt
			ci.MethodOrder = append(ci.MethodOrder, m.Method.Name)
		}
	}
}

// ── Declaration checking ─────────────────────────────────────────────────────

func (c *Checker) checkFnDecl(fn *ast.FnDecl) {
	ft := c.info.Funcs[fn.Name]
	c.retTy = ft.Return

	saved := c.scope
	c.scope = NewScope(c.scope)
	for i, p := range fn.Params {
		pt := ft.Params[i]
		c.scope.Define(&Symbol{Name: p.Name, Kind: SymParam, Type: pt})
	}
	c.checkBlock(fn.Body)
	c.scope = saved
	c.retTy = nil
}

func (c *Checker) checkClassDecl(cl *ast.ClassDecl) {
	ci := c.info.Classes[cl.Name]
	for _, m := range cl.Members {
		if m.Method == nil {
			continue
		}
		mt := ci.Methods[m.Method.Name]

		savedRet := c.retTy
		c.retTy = mt.Return
		savedScope := c.scope
		c.scope = NewScope(c.scope)

		// 'this' refers to the class instance
		c.scope.Define(&Symbol{Name: "this", Kind: SymVar,
			Type: ClassType(cl.Name)})
		for i, p := range m.Method.Params {
			c.scope.Define(&Symbol{Name: p.Name, Kind: SymParam,
				Type: mt.Params[i]})
		}
		c.checkBlock(m.Method.Body)
		c.scope = savedScope
		c.retTy = savedRet
	}
}

func (c *Checker) checkVarDecl(vd *ast.VarDecl) {
	valTy := c.checkExpr(vd.Value)
	var declTy *Type
	if vd.Ann != nil {
		declTy = c.resolveTypeExpr(vd.Ann)
		// Allow [] (void[]) to satisfy any array annotation.
		emptyArray := valTy.Kind == KindArray && valTy.Elem.Kind == KindVoid &&
			declTy.Kind == KindArray
		// Allow {} (map<void,void>) to satisfy any map annotation.
		emptyMap := valTy.Kind == KindMap && valTy.Key.Kind == KindVoid &&
			declTy.Kind == KindMap
		if emptyMap {
			// Propagate annotation type into the MapLit so codegen knows key/value types.
			if ml := extractMapLit(vd.Value); ml != nil {
				ml.ResolvedType = declTy
			}
		}
		if !emptyArray && !emptyMap && !declTy.Equal(valTy) {
			c.errorf(vd.Pos, "variable %q: declared type %s but got %s",
				vd.Name, declTy, valTy)
		}
	} else {
		declTy = valTy
	}
	c.scope.Define(&Symbol{Name: vd.Name, Kind: SymVar, Type: declTy})
}

// extractMapLit walks a bare expression tree to find a MapLit with no operators.
func extractMapLit(e *ast.Expr) *ast.MapLit {
	if e == nil || e.Assignment == nil || e.Assignment.Right != nil {
		return nil
	}
	o := e.Assignment.Left
	if o == nil || len(o.Rest) != 0 {
		return nil
	}
	and := o.Left
	if and == nil || len(and.Rest) != 0 {
		return nil
	}
	eq := and.Left
	if eq == nil || len(eq.Rest) != 0 {
		return nil
	}
	cmp := eq.Left
	if cmp == nil || len(cmp.Rest) != 0 {
		return nil
	}
	add := cmp.Left
	if add == nil || len(add.Rest) != 0 {
		return nil
	}
	mul := add.Left
	if mul == nil || len(mul.Rest) != 0 {
		return nil
	}
	u := mul.Left
	if u == nil || u.Op != "" {
		return nil
	}
	post := u.Post
	if post == nil || len(post.Ops) != 0 {
		return nil
	}
	return post.Base.MapLit
}

// ── Statement checking ───────────────────────────────────────────────────────

func (c *Checker) checkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	saved := c.scope
	c.scope = NewScope(c.scope)
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.scope = saved
}

func (c *Checker) checkStmt(s *ast.StmtNode) {
	switch {
	case s.VarDecl != nil:
		c.checkVarDecl(s.VarDecl)
	case s.Return != nil:
		var got *Type
		if s.Return.Value != nil {
			got = c.checkExpr(s.Return.Value)
		} else {
			got = Void
		}
		if c.retTy != nil && !c.retTy.Equal(got) {
			c.errorf(s.Return.Pos, "return type mismatch: expected %s, got %s", c.retTy, got)
		}
	case s.If != nil:
		c.checkIfStmt(s.If)
	case s.While != nil:
		condTy := c.checkExpr(s.While.Cond)
		if condTy.Kind != KindBool {
			c.errorf(s.While.Pos, "while condition must be bool, got %s", condTy)
		}
		savedLoop := c.inLoop
		c.inLoop = true
		c.checkBlock(s.While.Body)
		c.inLoop = savedLoop
	case s.For != nil:
		c.checkForStmt(s.For)
	case s.Break != nil:
		if !c.inLoop {
			c.errorf(s.Break.Pos, "break outside loop")
		}
	case s.Continue != nil:
		if !c.inLoop {
			c.errorf(s.Continue.Pos, "continue outside loop")
		}
	case s.ExprStmt != nil:
		c.checkExpr(s.ExprStmt.Expr)
	}
}

func (c *Checker) checkIfStmt(s *ast.IfStmt) {
	condTy := c.checkExpr(s.Cond)
	if condTy.Kind != KindBool {
		c.errorf(s.Pos, "if condition must be bool, got %s", condTy)
	}
	c.checkBlock(s.Then)
	if s.Else != nil {
		if s.Else.ElseIf != nil {
			c.checkIfStmt(s.Else.ElseIf)
		} else {
			c.checkBlock(s.Else.Block)
		}
	}
}

func (c *Checker) checkForStmt(s *ast.ForStmt) {
	saved := c.scope
	c.scope = NewScope(c.scope)

	if s.Init != nil {
		if s.Init.VarDecl != nil {
			vd := s.Init.VarDecl
			valTy := c.checkExpr(vd.Value)
			declTy := valTy
			if vd.Ann != nil {
				declTy = c.resolveTypeExpr(vd.Ann)
				if !declTy.Equal(valTy) {
					c.errorf(s.Init.Pos, "for init: declared %s but got %s", declTy, valTy)
				}
			}
			c.scope.Define(&Symbol{Name: vd.Name, Kind: SymVar, Type: declTy})
		} else if s.Init.Expr != nil {
			c.checkExpr(s.Init.Expr)
		}
	}
	if s.Cond != nil {
		ct := c.checkExpr(s.Cond)
		if ct.Kind != KindBool {
			c.errorf(s.Cond.Pos, "for condition must be bool, got %s", ct)
		}
	}
	if s.Post != nil {
		c.checkExpr(s.Post)
	}

	savedLoop := c.inLoop
	c.inLoop = true
	c.checkBlock(s.Body)
	c.inLoop = savedLoop
	c.scope = saved
}

// ── Expression checking ──────────────────────────────────────────────────────

func (c *Checker) checkExpr(e *ast.Expr) *Type {
	if e == nil {
		return Void
	}
	t := c.checkAssign(e.Assignment)
	c.setType(e, t)
	return t
}

func (c *Checker) checkAssign(a *ast.AssignExpr) *Type {
	if a == nil {
		return Void
	}
	leftTy := c.checkOr(a.Left)
	if a.Right != nil {
		rightTy := c.checkAssign(a.Right)
		if !leftTy.Equal(rightTy) {
			c.errorf(a.Pos, "assignment type mismatch: %s = %s", leftTy, rightTy)
		}
	}
	return leftTy
}

func (c *Checker) checkOr(a *ast.OrExpr) *Type {
	t := c.checkAnd(a.Left)
	for _, r := range a.Rest {
		rt := c.checkAnd(r.Right)
		if t.Kind != KindBool || rt.Kind != KindBool {
			c.errorf(a.Pos, "|| requires bool operands, got %s and %s", t, rt)
		}
		t = Bool
	}
	return t
}

func (c *Checker) checkAnd(a *ast.AndExpr) *Type {
	t := c.checkEq(a.Left)
	for _, r := range a.Rest {
		rt := c.checkEq(r.Right)
		if t.Kind != KindBool || rt.Kind != KindBool {
			c.errorf(a.Pos, "&& requires bool operands, got %s and %s", t, rt)
		}
		t = Bool
	}
	return t
}

func (c *Checker) checkEq(a *ast.EqExpr) *Type {
	t := c.checkCmp(a.Left)
	for _, r := range a.Rest {
		rt := c.checkCmp(r.Right)
		if !t.Equal(rt) {
			c.errorf(a.Left.Pos, "%s requires same-type operands, got %s and %s", r.Op, t, rt)
		}
		t = Bool
	}
	return t
}

func (c *Checker) checkCmp(a *ast.CmpExpr) *Type {
	t := c.checkAdd(a.Left)
	for _, r := range a.Rest {
		rt := c.checkAdd(r.Right)
		if !t.Equal(rt) {
			c.errorf(a.Left.Pos, "%s requires same-type operands, got %s and %s", r.Op, t, rt)
		}
		if t.Kind != KindInt && t.Kind != KindFloat && t.Kind != KindString {
			c.errorf(a.Left.Pos, "%s not valid for type %s", r.Op, t)
		}
		t = Bool
	}
	return t
}

func (c *Checker) checkAdd(a *ast.AddExpr) *Type {
	t := c.checkMul(a.Left)
	for _, r := range a.Rest {
		rt := c.checkMul(r.Right)
		if r.Op == "+" && t.Kind == KindString && rt.Kind == KindString {
			t = String
			continue
		}
		if !t.Equal(rt) {
			c.errorf(a.Left.Pos, "%s type mismatch: %s and %s", r.Op, t, rt)
		}
		if t.Kind != KindInt && t.Kind != KindFloat {
			c.errorf(a.Left.Pos, "%s not valid for type %s", r.Op, t)
		}
	}
	return t
}

func (c *Checker) checkMul(a *ast.MulExpr) *Type {
	t := c.checkUnary(a.Left)
	for _, r := range a.Rest {
		rt := c.checkUnary(r.Right)
		if !t.Equal(rt) {
			c.errorf(a.Left.Pos, "%s type mismatch: %s and %s", r.Op, t, rt)
		}
		if t.Kind != KindInt && t.Kind != KindFloat {
			c.errorf(a.Left.Pos, "%s not valid for type %s", r.Op, t)
		}
	}
	return t
}

func (c *Checker) checkUnary(u *ast.UnaryExpr) *Type {
	if u.Op != "" {
		t := c.checkUnary(u.Expr)
		switch u.Op {
		case "!":
			if t.Kind != KindBool {
				c.errorf(u.Pos, "! requires bool, got %s", t)
			}
			return Bool
		case "-":
			if t.Kind != KindInt && t.Kind != KindFloat {
				c.errorf(u.Pos, "unary - requires int or float, got %s", t)
			}
			return t
		}
	}
	return c.checkPostfix(u.Post)
}

func (c *Checker) checkPostfix(p *ast.PostfixExpr) *Type {
	t := c.checkPrimary(p.Base)
	for _, op := range p.Ops {
		switch {
		case op.Call != nil:
			t = c.checkCall(p.Base, t, op.Call)
		case op.Field != "":
			t = c.checkFieldAccess(t, op.Field, op.Pos)
		case op.Index != nil:
			if t.Kind == KindArray {
				idxTy := c.checkExpr(op.Index)
				if idxTy.Kind != KindInt {
					c.errorf(op.Pos, "array index must be int, got %s", idxTy)
				}
				t = t.Elem
			} else if t.Kind == KindMap {
				idxTy := c.checkExpr(op.Index)
				if !idxTy.Equal(t.Key) {
					c.errorf(op.Pos, "map key type mismatch: expected %s, got %s", t.Key, idxTy)
				}
				t = t.Value
			} else {
				c.errorf(op.Pos, "index on non-array/non-map type %s", t)
				t = Void
			}
		case op.PlusPlus || op.MinusMinus:
			if t.Kind != KindInt && t.Kind != KindFloat {
				c.errorf(op.Pos, "++ / -- requires numeric type, got %s", t)
			}
		}
	}
	return t
}

func (c *Checker) checkCall(base *ast.PrimaryExpr, calleeTy *Type, call *ast.CallArgs) *Type {
	// Special builtins
	if base != nil && base.Ident != "" {
		switch base.Ident {
		case "print", "write":
			if len(call.Args) != 1 {
				c.errorf(call.Pos, "%s() takes exactly 1 argument", base.Ident)
			} else {
				c.checkExpr(call.Args[0])
			}
			return Void
		case "println":
			return Void
		case "len":
			if len(call.Args) != 1 {
				c.errorf(call.Pos, "len() takes exactly 1 argument")
			} else {
				at := c.checkExpr(call.Args[0])
				if at.Kind != KindArray && at.Kind != KindString {
					c.errorf(call.Pos, "len() requires array or string, got %s", at)
				}
			}
			return Int
		case "push":
			if len(call.Args) != 2 {
				c.errorf(call.Pos, "push() takes 2 arguments")
			} else {
				c.checkExpr(call.Args[0])
				c.checkExpr(call.Args[1])
			}
			return Void
		case "int_to_string":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return String
		case "float_to_string":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return String
		case "char_at":
			if len(call.Args) == 2 {
				c.checkExpr(call.Args[0])
				c.checkExpr(call.Args[1])
			}
			return Int
		case "string_slice":
			if len(call.Args) == 3 {
				c.checkExpr(call.Args[0])
				c.checkExpr(call.Args[1])
				c.checkExpr(call.Args[2])
			}
			return String
		case "char_to_string":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return String
		case "read_file":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return String
		case "write_file":
			for _, a := range call.Args {
				c.checkExpr(a)
			}
			return Void
		case "exit":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return Void
		case "panic":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return Void
		case "args":
			return ArrayOf(String)
		case "print_err":
			if len(call.Args) == 1 {
				c.checkExpr(call.Args[0])
			}
			return Void
		case "read_stdin":
			return String

		case "map_set":
			if len(call.Args) != 3 {
				c.errorf(call.Pos, "map_set() takes 3 arguments (map, key, value)")
			} else {
				c.checkExpr(call.Args[0])
				c.checkExpr(call.Args[1])
				c.checkExpr(call.Args[2])
			}
			return Void
		case "map_get":
			if len(call.Args) != 2 {
				c.errorf(call.Pos, "map_get() takes 2 arguments (map, key)")
				return Void
			}
			mt := c.checkExpr(call.Args[0])
			c.checkExpr(call.Args[1])
			if mt.Kind == KindMap {
				return mt.Value
			}
			return Void
		case "map_has":
			if len(call.Args) != 2 {
				c.errorf(call.Pos, "map_has() takes 2 arguments (map, key)")
			} else {
				c.checkExpr(call.Args[0])
				c.checkExpr(call.Args[1])
			}
			return Bool
		case "map_delete":
			if len(call.Args) != 2 {
				c.errorf(call.Pos, "map_delete() takes 2 arguments (map, key)")
			} else {
				c.checkExpr(call.Args[0])
				c.checkExpr(call.Args[1])
			}
			return Void
		case "map_len":
			if len(call.Args) != 1 {
				c.errorf(call.Pos, "map_len() takes 1 argument")
			} else {
				c.checkExpr(call.Args[0])
			}
			return Int
		case "map_keys":
			if len(call.Args) != 1 {
				c.errorf(call.Pos, "map_keys() takes 1 argument")
				return ArrayOf(Void)
			}
			mt := c.checkExpr(call.Args[0])
			if mt.Kind == KindMap {
				return ArrayOf(mt.Key)
			}
			return ArrayOf(Void)
		}
	}

	// Regular function call
	for _, a := range call.Args {
		c.checkExpr(a)
	}
	if calleeTy == nil || calleeTy.Kind != KindFunc {
		return Void
	}
	if calleeTy.Return == nil {
		return Void
	}
	return calleeTy.Return
}

func (c *Checker) checkFieldAccess(recv *Type, field string, pos lexer.Position) *Type {
	if recv.Kind != KindClass {
		c.errorf(pos, "field access on non-class type %s", recv)
		return Void
	}
	ci, ok := c.info.Classes[recv.ClassName]
	if !ok {
		c.errorf(pos, "unknown class %s", recv.ClassName)
		return Void
	}
	if ft, ok := ci.Fields[field]; ok {
		return ft
	}
	if mt, ok := ci.Methods[field]; ok {
		return mt
	}
	c.errorf(pos, "class %s has no field or method %q", recv.ClassName, field)
	return Void
}

func (c *Checker) checkPrimary(p *ast.PrimaryExpr) *Type {
	if p == nil {
		return Void
	}
	switch {
	case p.Int != "":
		return Int
	case p.Float != "":
		return Float
	case p.Bool != "":
		return Bool
	case p.String != nil:
		return String
	case p.Null:
		return Null
	case p.This:
		if sym, ok := c.scope.Lookup("this"); ok {
			return sym.Type
		}
		c.errorf(p.Pos, "'this' used outside of class method")
		return Void
	case p.Ident != "":
		sym, ok := c.scope.Lookup(p.Ident)
		if !ok {
			c.errorf(p.Pos, "undefined: %s", p.Ident)
			return Void
		}
		return sym.Type
	case p.Array != nil:
		return c.checkArrayLit(p.Array)
	case p.MapLit != nil:
		t := c.checkMapLit(p.MapLit)
		p.MapLit.ResolvedType = t
		return t
	case p.FnExpr != nil:
		return c.checkFnExpr(p.FnExpr)
	case p.NewExpr != nil:
		return c.checkNewExpr(p.NewExpr)
	case p.Paren != nil:
		return c.checkExpr(p.Paren)
	}
	return Void
}

func (c *Checker) checkMapLit(m *ast.MapLit) *Type {
	if len(m.Entries) == 0 {
		return MapOf(Void, Void) // type inferred from annotation in checkVarDecl
	}
	keyTy := c.checkExpr(m.Entries[0].Key)
	valTy := c.checkExpr(m.Entries[0].Value)
	for _, e := range m.Entries[1:] {
		kt := c.checkExpr(e.Key)
		vt := c.checkExpr(e.Value)
		if !keyTy.Equal(kt) {
			c.errorf(m.Pos, "map literal has mixed key types: %s and %s", keyTy, kt)
		}
		if !valTy.Equal(vt) {
			c.errorf(m.Pos, "map literal has mixed value types: %s and %s", valTy, vt)
		}
	}
	return MapOf(keyTy, valTy)
}

func (c *Checker) checkArrayLit(a *ast.ArrayLit) *Type {
	if len(a.Elems) == 0 {
		return ArrayOf(Void) // empty array — element type inferred from context later
	}
	elemTy := c.checkExpr(a.Elems[0])
	for _, e := range a.Elems[1:] {
		et := c.checkExpr(e)
		if !elemTy.Equal(et) {
			c.errorf(a.Pos, "array literal has mixed types: %s and %s", elemTy, et)
		}
	}
	return ArrayOf(elemTy)
}

func (c *Checker) checkFnExpr(fn *ast.FnExpr) *Type {
	var params []*Type
	for _, p := range fn.Params {
		params = append(params, c.resolveTypeExpr(p.Type))
	}
	ret := c.resolveTypeExpr(fn.Ret)

	savedRet := c.retTy
	c.retTy = ret
	savedScope := c.scope
	c.scope = NewScope(c.scope)
	for i, p := range fn.Params {
		c.scope.Define(&Symbol{Name: p.Name, Kind: SymParam, Type: params[i]})
	}
	c.checkBlock(fn.Body)
	c.scope = savedScope
	c.retTy = savedRet

	return FuncType(params, ret)
}

func (c *Checker) checkNewExpr(n *ast.NewExpr) *Type {
	ci, ok := c.info.Classes[n.ClassName]
	if !ok {
		c.errorf(n.Pos, "unknown class %s", n.ClassName)
		return Void
	}
	// Find constructor (method named "new" or "init", or match field count)
	if _, hasNew := ci.Methods["new"]; hasNew {
		mt := ci.Methods["new"]
		if len(n.Args) != len(mt.Params) {
			c.errorf(n.Pos, "new %s: expected %d args, got %d",
				n.ClassName, len(mt.Params), len(n.Args))
		}
	}
	for _, a := range n.Args {
		c.checkExpr(a)
	}
	return ClassType(n.ClassName)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// FormatErrors formats checker errors with source context.
func FormatErrors(errs []CheckError) string {
	var sb strings.Builder
	for _, e := range errs {
		sb.WriteString(e.Error())
		sb.WriteString("\n")
	}
	return sb.String()
}

// Suppress unused import warning — strconv used in potential future expansion.
var _ = strconv.Itoa
