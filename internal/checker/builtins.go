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
	// string_contains(s, sub): bool
	s.Define(&Symbol{Name: "string_contains", Kind: SymFunc,
		Type: FuncType([]*Type{String, String}, Bool)})
	// string_starts_with(s, prefix): bool
	s.Define(&Symbol{Name: "string_starts_with", Kind: SymFunc,
		Type: FuncType([]*Type{String, String}, Bool)})
	// string_ends_with(s, suffix): bool
	s.Define(&Symbol{Name: "string_ends_with", Kind: SymFunc,
		Type: FuncType([]*Type{String, String}, Bool)})
	// string_index_of(s, sub): int  — first index of sub in s, or -1
	s.Define(&Symbol{Name: "string_index_of", Kind: SymFunc,
		Type: FuncType([]*Type{String, String}, Int)})
	// string_to_int(s): int  — parse decimal integer string
	s.Define(&Symbol{Name: "string_to_int", Kind: SymFunc,
		Type: FuncType([]*Type{String}, Int)})
	// read_bytes(n): string  — read exactly n bytes from stdin
	s.Define(&Symbol{Name: "read_bytes", Kind: SymFunc,
		Type: FuncType([]*Type{Int}, String)})
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
	// getenv(name): string — read an environment variable; returns "" if unset
	s.Define(&Symbol{Name: "getenv", Kind: SymFunc,
		Type: FuncType([]*Type{String}, String)})
	// delete_file(path): void — remove a file; silently ignores errors
	s.Define(&Symbol{Name: "delete_file", Kind: SymFunc,
		Type: FuncType([]*Type{String}, Void)})
	// is_dir(path): bool — true if path is an existing directory
	s.Define(&Symbol{Name: "is_dir", Kind: SymFunc,
		Type: FuncType([]*Type{String}, Bool)})
	// list_dir(path): string[] — sorted filenames (not full paths) in a directory
	s.Define(&Symbol{Name: "list_dir", Kind: SymFunc,
		Type: FuncType([]*Type{String}, ArrayOf(String))})
	// runtime_lib_path(): string — path to jerry_runtime.a for the current install
	s.Define(&Symbol{Name: "runtime_lib_path", Kind: SymFunc,
		Type: FuncType([]*Type{}, String)})
	// stdlib_dir_path(): string — path to the stdlib directory for the current install
	s.Define(&Symbol{Name: "stdlib_dir_path", Kind: SymFunc,
		Type: FuncType([]*Type{}, String)})
	// exec(args): int — run a subprocess; returns exit code
	s.Define(&Symbol{Name: "exec", Kind: SymFunc,
		Type: FuncType([]*Type{ArrayOf(String)}, Int)})
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
	// map_set(m, key, val): void
	s.Define(&Symbol{Name: "map_set", Kind: SymFunc,
		Type: FuncType([]*Type{MapOf(Void, Void), Void, Void}, Void)})
	// map_get(m, key): V  — return type is map's value type; checked per-call
	s.Define(&Symbol{Name: "map_get", Kind: SymFunc,
		Type: FuncType([]*Type{MapOf(Void, Void), Void}, Void)})
	// map_has(m, key): bool
	s.Define(&Symbol{Name: "map_has", Kind: SymFunc,
		Type: FuncType([]*Type{MapOf(Void, Void), Void}, Bool)})
	// map_delete(m, key): void
	s.Define(&Symbol{Name: "map_delete", Kind: SymFunc,
		Type: FuncType([]*Type{MapOf(Void, Void), Void}, Void)})
	// map_len(m): int
	s.Define(&Symbol{Name: "map_len", Kind: SymFunc,
		Type: FuncType([]*Type{MapOf(Void, Void)}, Int)})
	// map_keys(m): K[]  — return type is array of key type; checked per-call
	s.Define(&Symbol{Name: "map_keys", Kind: SymFunc,
		Type: FuncType([]*Type{MapOf(Void, Void)}, ArrayOf(Void))})
}
