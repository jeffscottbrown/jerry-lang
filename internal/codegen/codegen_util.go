package codegen

import (
	"fmt"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
)

// ── Temporary and label counters ──────────────────────────────────────────────

func (g *Generator) newTmp() string {
	g.tmp++
	return fmt.Sprintf("%%t%d", g.tmp)
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

// ── Alloca / store helpers ────────────────────────────────────────────────────

func (g *Generator) allocaInto(out *strings.Builder, name, llvmTy string) string {
	g.tmp++
	reg := fmt.Sprintf("%%%s.%d", name, g.tmp)
	fmt.Fprintf(out, "  %s = alloca %s\n", reg, llvmTy)
	return reg
}

func (g *Generator) storeInto(out *strings.Builder, llvmTy, val, reg string) {
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", llvmTy, val, reg)
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

// ── Function context save / restore ──────────────────────────────────────────

type genContext struct {
	curFnName      string
	retType        *checker.Type
	locals         map[string]*localVar
	labelStack     []loopLabels
	terminated     bool
	tmp            int
	lbl            int
	releaseScopes  [][]releaseEntry
	loopScopeDepth []int
}

func (g *Generator) saveContext() genContext {
	return genContext{
		curFnName:      g.curFnName,
		retType:        g.retType,
		locals:         g.locals,
		labelStack:     g.labelStack,
		terminated:     g.terminated,
		tmp:            g.tmp,
		lbl:            g.lbl,
		releaseScopes:  g.releaseScopes,
		loopScopeDepth: g.loopScopeDepth,
	}
}

func (g *Generator) restoreContext(ctx genContext) {
	g.curFnName     = ctx.curFnName
	g.retType       = ctx.retType
	g.locals        = ctx.locals
	g.labelStack    = ctx.labelStack
	g.terminated    = ctx.terminated
	g.releaseScopes  = ctx.releaseScopes
	g.loopScopeDepth = ctx.loopScopeDepth
	// Don't restore tmp/lbl — keep them globally unique.
}

func (g *Generator) cloneLocals() map[string]*localVar {
	m := make(map[string]*localVar, len(g.locals))
	for k, v := range g.locals {
		m[k] = v
	}
	return m
}

// ── Reference-counting scope helpers ─────────────────────────────────────────

// releaseEntry records a heap-type local variable that needs jerry_release
// when its enclosing scope exits.
type releaseEntry struct {
	allocaReg string // e.g. "%a.1" — the alloca holding the pointer
	ty        *checker.Type
}

// isHeapType reports whether ty requires reference counting.
func isHeapType(ty *checker.Type) bool {
	if ty == nil {
		return false
	}
	switch ty.Kind {
	case checker.KindString, checker.KindArray, checker.KindClass:
		return true
	}
	return false
}

// pushReleaseScope opens a new scope level for tracking heap locals.
func (g *Generator) pushReleaseScope() {
	g.releaseScopes = append(g.releaseScopes, nil)
}

// popReleaseScope removes the innermost scope without emitting releases.
// Callers must emit releases (or have already done so via break/continue/return)
// before calling this.
func (g *Generator) popReleaseScope() {
	if len(g.releaseScopes) > 0 {
		g.releaseScopes = g.releaseScopes[:len(g.releaseScopes)-1]
	}
}

// registerHeapLocal records a newly-defined heap-type local in the current scope.
func (g *Generator) registerHeapLocal(allocaReg string, ty *checker.Type) {
	if len(g.releaseScopes) == 0 {
		return
	}
	last := len(g.releaseScopes) - 1
	g.releaseScopes[last] = append(g.releaseScopes[last], releaseEntry{allocaReg: allocaReg, ty: ty})
}

// emitReleasesForScope emits jerry_release for all heap locals in scope[idx],
// in reverse declaration order (last-in-first-out). exceptAllocaReg, if non-empty,
// is skipped (used to exempt the value being returned from a function).
func (g *Generator) emitReleasesForScope(idx int, exceptAllocaReg string, out *strings.Builder) {
	scope := g.releaseScopes[idx]
	for i := len(scope) - 1; i >= 0; i-- {
		re := scope[i]
		if re.allocaReg == exceptAllocaReg {
			continue
		}
		tmp := g.newTmp()
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", tmp, re.allocaReg)
		fmt.Fprintf(out, "  call void @jerry_release(ptr %s)\n", tmp)
	}
}

// emitCurrentScopeReleases emits releases for the innermost scope.
func (g *Generator) emitCurrentScopeReleases(out *strings.Builder) {
	if len(g.releaseScopes) == 0 {
		return
	}
	g.emitReleasesForScope(len(g.releaseScopes)-1, "", out)
}

// emitAllReleases emits releases for all scopes from innermost to outermost.
// exceptAllocaReg is the alloca of a local being returned (exempt from release).
func (g *Generator) emitAllReleases(exceptAllocaReg string, out *strings.Builder) {
	for i := len(g.releaseScopes) - 1; i >= 0; i-- {
		g.emitReleasesForScope(i, exceptAllocaReg, out)
	}
}

// emitLoopBoundaryReleases emits releases for all scopes from the current
// innermost scope down to (and including) loopDepth. Used by break/continue.
func (g *Generator) emitLoopBoundaryReleases(loopDepth int, out *strings.Builder) {
	for i := len(g.releaseScopes) - 1; i >= loopDepth; i-- {
		g.emitReleasesForScope(i, "", out)
	}
}

// bareStringLit returns the string value if expr is a bare string literal with
// no operators or postfix operations, or nil otherwise. Used to detect cases
// where a string literal's lifetime is managed directly by the caller (var
// initializer, return statement) rather than needing a hidden release scope.
func bareStringLit(e *ast.Expr) *string {
	if e == nil {
		return nil
	}
	a := e.Assignment
	if a == nil || a.Right != nil {
		return nil
	}
	or := a.Left
	if or == nil || len(or.Rest) != 0 {
		return nil
	}
	and := or.Left
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
	prim := post.Base
	if prim == nil {
		return nil
	}
	return prim.String
}

// endsWithIndex returns true if expr is a bare postfix expression whose last
// operation is an array index (e.g. names[0]). Used by genVarDecl to detect
// borrowed references from arrays so a retain can be issued before the
// variable takes ownership.
func endsWithIndex(e *ast.Expr) bool {
	if e == nil {
		return false
	}
	a := e.Assignment
	if a == nil || a.Right != nil {
		return false
	}
	or := a.Left
	if or == nil || len(or.Rest) != 0 {
		return false
	}
	and := or.Left
	if and == nil || len(and.Rest) != 0 {
		return false
	}
	eq := and.Left
	if eq == nil || len(eq.Rest) != 0 {
		return false
	}
	cmp := eq.Left
	if cmp == nil || len(cmp.Rest) != 0 {
		return false
	}
	add := cmp.Left
	if add == nil || len(add.Rest) != 0 {
		return false
	}
	mul := add.Left
	if mul == nil || len(mul.Rest) != 0 {
		return false
	}
	u := mul.Left
	if u == nil || u.Op != "" {
		return false
	}
	post := u.Post
	if post == nil || len(post.Ops) == 0 {
		return false
	}
	lastOp := post.Ops[len(post.Ops)-1]
	return lastOp.Index != nil
}

// simpleIdent returns the variable name if expr is a bare identifier with no
// operators or postfix operations, or "" otherwise. Used by genReturn to
// determine whether the return value is a heap local (and should be exempted
// from the scope-cleanup release rather than retained).
func simpleIdent(e *ast.Expr) string {
	if e == nil {
		return ""
	}
	a := e.Assignment
	if a == nil || a.Right != nil {
		return ""
	}
	or := a.Left
	if or == nil || len(or.Rest) != 0 {
		return ""
	}
	and := or.Left
	if and == nil || len(and.Rest) != 0 {
		return ""
	}
	eq := and.Left
	if eq == nil || len(eq.Rest) != 0 {
		return ""
	}
	cmp := eq.Left
	if cmp == nil || len(cmp.Rest) != 0 {
		return ""
	}
	add := cmp.Left
	if add == nil || len(add.Rest) != 0 {
		return ""
	}
	mul := add.Left
	if mul == nil || len(mul.Rest) != 0 {
		return ""
	}
	u := mul.Left
	if u == nil || u.Op != "" {
		return ""
	}
	post := u.Post
	if post == nil || len(post.Ops) != 0 {
		return ""
	}
	prim := post.Base
	if prim == nil {
		return ""
	}
	return prim.Ident
}

// ── String escaping ───────────────────────────────────────────────────────────

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
