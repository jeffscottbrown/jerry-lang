// Package codegen emits LLVM IR (textual form) from a type-checked Jerry AST.
package codegen

import (
	"fmt"
	"strings"

	"github.com/jeffscottbrown/jerry-lang/internal/ast"
	"github.com/jeffscottbrown/jerry-lang/internal/checker"
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
	curFnName  string
	retType    *checker.Type
	locals     map[string]*localVar // name → alloca register + type
	labelStack []loopLabels         // for break/continue

	// Tracks whether current basic block has a terminator
	terminated bool

	// Name of the current basic block (without %)
	curBlock string

	// Reference-counting: heap locals to release at block exit.
	// Each entry is one scope level; genBlock pushes/pops.
	releaseScopes [][]releaseEntry
	// For each active loop, the releaseScopes depth at loop entry.
	// Used by break/continue to know which scopes to clean up.
	loopScopeDepth []int

	globals     map[string]*localVar // global variable slots, name → @name
	globalDecls strings.Builder      // LLVM global variable declarations
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
	g := &Generator{info: info, globals: make(map[string]*localVar)}
	if err := g.genPrograms(progs); err != nil {
		return "", err
	}
	return g.out.String(), nil
}

func (g *Generator) genPrograms(progs []*ast.Program) error {
	var fnBuf strings.Builder
	g.out = strings.Builder{}

	g.emitClassTypeDecls()

	// Pre-register all global variable declarations so function bodies can reference them.
	var globalVarDecls []*ast.VarDecl
	for _, prog := range progs {
		for _, tl := range prog.Stmts {
			if tl.VarDecl != nil {
				globalVarDecls = append(globalVarDecls, tl.VarDecl)
			}
		}
	}
	for _, vd := range globalVarDecls {
		ty := g.exprType(vd.Value)
		lt := g.llvmType(ty)
		zero := g.zeroValue(ty)
		fmt.Fprintf(&g.globalDecls, "@%s = global %s %s\n", vd.Name, lt, zero)
		g.globals[vd.Name] = &localVar{reg: "@" + vd.Name, llvmTy: lt, altType: ty}
	}

	// Generate class destructor functions (needed before any new-expr can reference them).
	for _, prog := range progs {
		for _, tl := range prog.Stmts {
			if tl.Class != nil {
				if err := g.genClassDestructor(tl.Class, &fnBuf); err != nil {
					return err
				}
			}
		}
	}

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
			}
		}
	}

	if len(globalVarDecls) > 0 {
		if err := g.genGlobalInitFn(globalVarDecls, &fnBuf); err != nil {
			return err
		}
	}

	// Assemble the final module.
	var mod strings.Builder
	mod.WriteString(g.moduleHeader())
	mod.WriteString(g.classTypes.String())
	mod.WriteString(g.runtimeDecls())
	if g.globalDecls.Len() > 0 {
		mod.WriteString("\n; ── Global variables ──────────────────────────────────────────────\n")
		mod.WriteString(g.globalDecls.String())
		mod.WriteString("\n")
	}
	for i, s := range g.strConsts {
		escaped := llvmEscapeString(s)
		n := len(s) + 1
		fmt.Fprintf(&mod, "@.str.%d = private unnamed_addr constant [%d x i8] c\"%s\\00\"\n", i, n, escaped)
	}
	mod.WriteString("\n")
	mod.WriteString(g.classVtableDecls())
	mod.WriteString(g.pendingFns.String())
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
; JerryMap  = { ptr buckets, i64 bucket_count, i64 len, i64 value_size, i8 string_keys }
%JerryStr   = type { i64, ptr }
%JerryArray = type { i64, i64, ptr, i64 }
%JerryMap   = type { ptr, i64, i64, i64, i8 }
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
declare i8   @jerry_string_contains(ptr, ptr)
declare i8   @jerry_string_starts_with(ptr, ptr)
declare i8   @jerry_string_ends_with(ptr, ptr)
declare i64  @jerry_string_index_of(ptr, ptr)
declare i64  @jerry_string_to_int(ptr)
declare ptr  @jerry_read_bytes(i64)
declare void @jerry_print_int(i64)
declare void @jerry_print_float(double)
declare void @jerry_print_bool(i8)
declare void @jerry_print_string(ptr)
declare void @jerry_print_array(ptr)
declare void @jerry_println()
declare ptr  @jerry_array_new(i64, i64, i8)
declare void @jerry_array_mark_heap(ptr)
declare ptr  @jerry_array_get(ptr, i64)
declare void @jerry_array_set(ptr, i64, ptr)
declare i64  @jerry_array_len(ptr)
declare void @jerry_array_push(ptr, ptr)
declare void @jerry_panic(ptr)
declare void @jerry_exit(i64)
declare void @jerry_retain(ptr)
declare void @jerry_release(ptr)
declare ptr  @jerry_alloc(i64)
declare ptr  @jerry_read_file(ptr)
declare void @jerry_write_file(ptr, ptr)
declare ptr  @jerry_getenv(ptr)
declare void @jerry_delete_file(ptr)
declare i8   @jerry_is_dir(ptr)
declare ptr  @jerry_list_dir(ptr)
declare ptr  @jerry_runtime_lib_path()
declare ptr  @jerry_stdlib_dir_path()
declare i64  @jerry_exec(ptr)
declare void @jerry_each_line(ptr, ptr)
declare void @jerry_capture_args(i64, ptr)
declare ptr  @jerry_args()
declare void @jerry_print_err(ptr)
declare ptr  @jerry_read_stdin()
declare i64  @jerry_now_millis()
declare i64  @jerry_now_seconds()
declare ptr  @jerry_now_string()
declare ptr  @jerry_map_new(i8, i64)
declare void @jerry_map_set(ptr, ptr, ptr)
declare ptr  @jerry_map_get(ptr, ptr)
declare i8   @jerry_map_has(ptr, ptr)
declare void @jerry_map_delete(ptr, ptr)
declare i64  @jerry_map_len(ptr)
declare ptr  @jerry_map_keys(ptr)

`
}

// ── Class type declarations ───────────────────────────────────────────────────

func (g *Generator) emitClassTypeDecls() {
	for name, ci := range g.info.Classes {
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
	var sb strings.Builder
	sb.WriteString(`
; ── C entry point ────────────────────────────────────────────────────────────
define i32 @main(i32 %argc, ptr %argv) {
entry:
  %argc64 = sext i32 %argc to i64
  call void @jerry_capture_args(i64 %argc64, ptr %argv)
`)
	if len(g.globals) > 0 {
		sb.WriteString("  call void @jerry_global_init()\n")
	}
	sb.WriteString(`  call void @main_jerry()
  ret i32 0
}
`)
	return sb.String()
}
