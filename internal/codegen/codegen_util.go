package codegen

import (
	"fmt"
	"strings"

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
