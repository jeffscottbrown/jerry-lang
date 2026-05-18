package checker

// newBuiltinScope returns a fresh scope containing all compiler-primitive builtins.
// It is the root of every scope chain in the system.
func newBuiltinScope() *Scope {
	s := NewScope(nil)
	installBuiltins(s)
	return s
}

// installBuiltins registers all compiler-primitive builtin functions into s.
func installBuiltins(s *Scope) {
	// print(x): void — polymorphic; resolved per-call in codegen
	s.Define(&Symbol{Name: "print", Kind: SymFunc,
		Type: FuncType([]*Type{Void}, Void)})
	// write(x): void — like print but no newline
	s.Define(&Symbol{Name: "write", Kind: SymFunc,
		Type: FuncType([]*Type{Void}, Void)})
	// println(): void
	s.Define(&Symbol{Name: "println", Kind: SymFunc,
		Type: FuncType([]*Type{}, Void)})
	// len(arr): int
	s.Define(&Symbol{Name: "len", Kind: SymFunc,
		Type: FuncType([]*Type{ArrayOf(Void)}, Int)})
	// push(arr, elem): void
	s.Define(&Symbol{Name: "push", Kind: SymFunc,
		Type: FuncType([]*Type{ArrayOf(Void), Void}, Void)})
	// int_to_string(n): string
	s.Define(&Symbol{Name: "int_to_string", Kind: SymFunc,
		Type: FuncType([]*Type{Int}, String)})
	// float_to_string(f): string
	s.Define(&Symbol{Name: "float_to_string", Kind: SymFunc,
		Type: FuncType([]*Type{Float}, String)})
	// char_at(s, i): int  — returns Unicode code point at index i
	s.Define(&Symbol{Name: "char_at", Kind: SymFunc,
		Type: FuncType([]*Type{String, Int}, Int)})
	// string_slice(s, start, end): string  — s[start:end]
	s.Define(&Symbol{Name: "string_slice", Kind: SymFunc,
		Type: FuncType([]*Type{String, Int, Int}, String)})
	// char_to_string(code): string  — single character from code point
	s.Define(&Symbol{Name: "char_to_string", Kind: SymFunc,
		Type: FuncType([]*Type{Int}, String)})
	// read_file(path): string
	s.Define(&Symbol{Name: "read_file", Kind: SymFunc,
		Type: FuncType([]*Type{String}, String)})
	// write_file(path, content): void
	s.Define(&Symbol{Name: "write_file", Kind: SymFunc,
		Type: FuncType([]*Type{String, String}, Void)})
	// exit(code): void
	s.Define(&Symbol{Name: "exit", Kind: SymFunc,
		Type: FuncType([]*Type{Int}, Void)})
	// panic(msg): void
	s.Define(&Symbol{Name: "panic", Kind: SymFunc,
		Type: FuncType([]*Type{String}, Void)})
	s.Define(&Symbol{Name: "each_line", Kind: SymFunc,
		Type: FuncType([]*Type{String, FuncType([]*Type{String}, Void)}, Void)})
	// args(): string[]
	s.Define(&Symbol{Name: "args", Kind: SymFunc,
		Type: FuncType([]*Type{}, ArrayOf(String))})
	// print_err(s: string): void
	s.Define(&Symbol{Name: "print_err", Kind: SymFunc,
		Type: FuncType([]*Type{String}, Void)})
	// read_stdin(): string
	s.Define(&Symbol{Name: "read_stdin", Kind: SymFunc,
		Type: FuncType([]*Type{}, String)})
	// now_millis(): int — Unix epoch in milliseconds
	s.Define(&Symbol{Name: "now_millis", Kind: SymFunc,
		Type: FuncType([]*Type{}, Int)})
	// now_seconds(): int — Unix epoch in seconds
	s.Define(&Symbol{Name: "now_seconds", Kind: SymFunc,
		Type: FuncType([]*Type{}, Int)})
	// now_string(): string — local time as "YYYY-MM-DD HH:MM:SS"
	s.Define(&Symbol{Name: "now_string", Kind: SymFunc,
		Type: FuncType([]*Type{}, String)})
}
