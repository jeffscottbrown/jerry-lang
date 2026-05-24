# The Jerry Language Guide

Jerry is a small, statically-typed, JavaScript-flavoured language that compiles
to native binaries through LLVM IR. The compiler is self-hosted — written in
Jerry itself — and ships as a `jerry` CLI alongside a `jerry-compiler` binary.

This guide is a practical tour: every section is short, every snippet is
runnable. Save any snippet as `foo.jer` and run it with:

```sh
jerry run foo.jer
```

> ✨ **Looking for a real-world program written in Jerry?**
> Check out **[gdgrep](https://github.com/jeffscottbrown/gdgrep)** — a fast,
> friendly `grep` replacement. It's installable in one command with Homebrew
> (`brew install jeffscottbrown/gdgrep/gdgrep`) and its source is a great
> reference for multi-file Jerry projects. See
> [Showcase: gdgrep](#showcase-gdgrep) below.

---

## Table of Contents

1. [Hello, Jerry](#hello-jerry)
2. [Primitive types](#primitive-types)
3. [Variables](#variables)
4. [Operators](#operators)
5. [Control flow](#control-flow)
6. [Functions](#functions)
7. [Closures and function values](#closures-and-function-values)
8. [Arrays](#arrays)
9. [Maps](#maps)
10. [Strings](#strings)
11. [Classes](#classes)
12. [Memory model](#memory-model)
13. [Testing](#testing)
14. [Built-in functions](#built-in-functions)
15. [The always-available core library](#the-always-available-core-library)
16. [Time and the `Timer` class](#time-and-the-timer-class)
17. [JSON with `@json`](#json-with-json)
18. [The `@string` stdlib module](#the-string-stdlib-module)
19. [Calling native code with `extern fn`](#calling-native-code-with-extern-fn)
20. [Modules and remote packages](#modules-and-remote-packages)
21. [The `jerry-string` remote module](#the-jerry-string-remote-module)
22. [The `jerry-logging` remote module](#the-jerry-logging-remote-module)
23. [Working program: end-to-end example](#working-program-end-to-end-example)
24. [Showcase: gdgrep](#showcase-gdgrep)
25. [CLI reference](#cli-reference)

---

## Hello, Jerry

Every Jerry program starts at `fn main()`.

```jerry
fn main() {
    print("Hello, Jerry!");
    print("2 + 2 = " + int_to_string(2 + 2));
}
```

```text
Hello, Jerry!
2 + 2 = 4
```

A few things worth noting up front:

- Statements end in `;` — except blocks (`{ ... }`).
- `print` always appends a newline. Use `write` for no newline, `println` for
  an empty line, and `print_err` to write to standard error.
- There is no implicit conversion from `int` to `string`. Concatenate with
  `int_to_string`, `float_to_string`, or `bool_to_string` (see
  [`stdlib/core.jer`](../stdlib/core.jer)).

---

## Primitive types

| Type     | Example literal       | Notes                                  |
|----------|-----------------------|----------------------------------------|
| `int`    | `42`, `-7`, `0`       | 64-bit signed integer                  |
| `float`  | `3.14`, `-0.5`        | 64-bit IEEE-754 double                 |
| `bool`   | `true`, `false`       | Result of comparisons and `&&` / `||`  |
| `string` | `"hello"`             | UTF-8, indexed by **byte/code point**  |
| `T[]`    | `[1, 2, 3]`           | Growable array of `T`                  |
| `void`   | —                     | Used as a function return type only    |

```jerry
fn main() {
    let n: int    = 42;
    let pi: float = 3.14159;
    let on: bool  = true;
    let s: string = "jerry";
    let xs: int[] = [1, 2, 3];

    print(int_to_string(n));
    print(float_to_string(pi));
    print(bool_to_string(on));
    print(s);
    print("xs has " + int_to_string(len(xs)) + " items");
}
```

---

## Variables

Variables are introduced with `let`. They are mutable; reassign with `=`.

```jerry
fn main() {
    let count: int = 0;
    count = count + 1;
    count++;          // shorthand for count = count + 1
    print(int_to_string(count));   // 2
}
```

Top-level `let` declarations create globals visible to every function in the
file (and to the module that defines them):

```jerry
let GREETING: string = "Hello";

fn main() {
    print(GREETING + ", world!");
}
```

---

## Operators

| Category       | Operators                                |
|----------------|------------------------------------------|
| Arithmetic     | `+`, `-`, `*`, `/`, `%`                  |
| Increment      | `++`, `--` (postfix statements)          |
| Comparison     | `==`, `!=`, `<`, `<=`, `>`, `>=`         |
| Logical        | `&&`, `||`, `!`                          |
| String concat  | `+`                                      |
| Indexing       | `arr[i]`                                 |
| Field access   | `obj.field`, `obj.method(...)`           |

The `+` operator concatenates two `string`s but cannot mix `string` and `int`
implicitly — convert first:

```jerry
fn main() {
    let n: int = 7;
    print("n = " + int_to_string(n));   // ok
    // print("n = " + n);                // type error
}
```

---

## Control flow

### `if` / `else`

Parentheses around the condition are optional.

```jerry
fn classify(n: int): string {
    if n < 0 {
        return "negative";
    } else if n == 0 {
        return "zero";
    }
    return "positive";
}
```

### `while`

```jerry
fn main() {
    let i: int = 0;
    while i < 3 {
        print("i = " + int_to_string(i));
        i++;
    }
}
```

### `for`

C-style `for (init; cond; post)`:

```jerry
fn main() {
    let nums: int[] = [10, 20, 30];
    for (let i: int = 0; i < len(nums); i++) {
        print("nums[" + int_to_string(i) + "] = " + int_to_string(nums[i]));
    }
}
```

### `break` / `continue`

Both work inside `while` and `for` loops.

---

## Functions

```jerry
fn add(a: int, b: int): int {
    return a + b;
}

fn greet(name: string): void {
    print("Hello, " + name);
}

fn main() {
    print(int_to_string(add(2, 3)));
    greet("Jerry");
}
```

- Parameter types are required.
- The return type follows `:`. Omit it (or use `: void`) for procedures.
- Recursion works exactly as you'd expect:

```jerry
fn fib(n: int): int {
    if n <= 1 {
        return n;
    }
    return fib(n - 1) + fib(n - 2);
}
```

See [`examples/fibonacci.jer`](../examples/fibonacci.jer).

---

## Closures and function values

Functions are first-class values. The type `fn(int): int` is the type of a
function taking one `int` and returning an `int`.

```jerry
fn apply(f: fn(int): int, x: int): int {
    return f(x);
}

fn main() {
    let double = fn(x: int): int { return x * 2; };
    let square = fn(x: int): int { return x * x; };

    print(int_to_string(apply(double, 5)));   // 10
    print(int_to_string(apply(square, 5)));   // 25
}
```

Function literals don't currently capture surrounding variables; pass any
needed state in as parameters. See [`examples/closures.jer`](../examples/closures.jer).

---

## Arrays

Arrays are heterogeneous-in-name-only: they hold exactly one element type,
declared as `T[]`. They are growable via `push`.

```jerry
fn main() {
    let nums: int[] = [10, 20, 30];

    push(nums, 40);

    let total: int = 0;
    for (let i: int = 0; i < len(nums); i++) {
        total = total + nums[i];
    }
    print("sum = " + int_to_string(total));   // 100
}
```

Useful built-ins for arrays:

- `len(arr)` — number of elements.
- `push(arr, x)` — append `x` to `arr` in place.
- `arr[i]` — index into the array (0-based).

---

## Maps

Maps are hash tables keyed by `string` or `int`, declared as
`map<KeyType, ValueType>`. An empty map literal is `{}`.

```jerry
fn main() {
    // string keys
    let scores: map<string, int> = {};
    map_set(scores, "alice", 95);
    map_set(scores, "bob",   87);
    print(int_to_string(map_get(scores, "alice")));  // 95

    // index operator works for both get and set
    scores["carol"] = 91;
    print(int_to_string(scores["carol"]));           // 91
}
```

### Map literals

Pre-populate a map with a brace literal:

```jerry
fn main() {
    let codes: map<string, int> = {"ok": 200, "not_found": 404, "error": 500};
    print(int_to_string(codes["ok"]));  // 200

    let labels: map<int, string> = {1: "one", 2: "two", 3: "three"};
    print(labels[2]);  // two
}
```

### int keys

Maps with `int` keys work identically — just declare the type:

```jerry
fn main() {
    let m: map<int, string> = {};
    map_set(m, 42, "answer");
    print(map_get(m, 42));  // answer
}
```

### Map builtins

| Builtin                              | Description                                   |
|--------------------------------------|-----------------------------------------------|
| `map_set(m, key, value): void`       | Insert or overwrite `key → value`             |
| `map_get(m, key): V`                 | Retrieve value for `key`                      |
| `map_has(m, key): bool`              | True if `key` is present                      |
| `map_delete(m, key): void`           | Remove `key` (no-op if absent)                |
| `map_len(m): int`                    | Number of entries                             |
| `map_keys(m): K[]`                   | Array of all keys (unordered)                 |

The index operator `m[key]` is sugar for `map_get` on the right-hand side and
`map_set` on the left-hand side.

### Iterating over a map

Use `map_keys` to iterate:

```jerry
fn main() {
    let word_count: map<string, int> = {"the": 3, "quick": 1, "brown": 1};
    let keys: string[] = map_keys(word_count);
    for (let i: int = 0; i < len(keys); i++) {
        let k: string = keys[i];
        print(k + ": " + int_to_string(map_get(word_count, k)));
    }
}
```

---

## Strings

Strings are UTF-8 and indexed by code-point position via `char_at`. They are
**immutable** — every transformation returns a new string.

```jerry
fn main() {
    let s: string = "Hello, Jerry!";

    print("len = " + int_to_string(len(s)));                    // 13
    print("char_at(0) = " + int_to_string(char_at(s, 0)));     // 72  ('H')
    print("char_to_string(72) = " + char_to_string(72));        // H
    print("slice = " + string_slice(s, 0, 5));                  // Hello
    print(bool_to_string(string_starts_with(s, "Hello")));      // true
    print(bool_to_string(string_ends_with(s, "Jerry!")));       // true
    print(bool_to_string(string_contains(s, "Jerry")));         // true
}
```

### Char literals

Single-character integer constants can be written with single quotes. The value
is the ASCII/Unicode code point of the character.

```jerry
fn count_newlines(s: string): int {
    let n: int = 0;
    for (let i: int = 0; i < len(s); i++) {
        if char_at(s, i) == '\n' { n++; }
    }
    return n;
}
```

Escape sequences `'\n'`, `'\t'`, `'\r'`, `'\\'`, `'\''`, and `'\0'` are
supported. A plain character like `'A'` evaluates to `65`.

### String transformation builtins

Case-folding and whitespace trimming are available as built-in functions (no
`include` required). For splitting, joining, and richer string operations see
the [`@string` stdlib module](#the-string-stdlib-module).

| Builtin | Description |
|---|---|
| `string_to_lower(s): string` | ASCII lowercase copy of `s` |
| `string_to_upper(s): string` | ASCII uppercase copy of `s` |
| `string_trim(s): string`     | Strip leading and trailing whitespace |
| `string_replace(s, from, to): string` | Replace all occurrences of `from` with `to` |

---

## Classes

Classes bundle fields and methods. The constructor is the method named `new`.
Inside a method, `this` refers to the instance.

```jerry
class Point {
    x: int;
    y: int;

    fn new(px: int, py: int) {
        this.x = px;
        this.y = py;
    }

    fn to_string(): string {
        return "(" + int_to_string(this.x) + ", " + int_to_string(this.y) + ")";
    }

    fn distance_sq(other: Point): int {
        let dx: int = this.x - other.x;
        let dy: int = this.y - other.y;
        return dx * dx + dy * dy;
    }
}

fn main() {
    let a: Point = new Point(3, 4);
    let b: Point = new Point(0, 0);

    print(a.to_string());                         // (3, 4)
    print(int_to_string(a.distance_sq(b)));       // 25
}
```

See [`examples/classes.jer`](../examples/classes.jer).

---

## Memory model

Jerry compiles to native code via LLVM IR. Understanding where values live
helps explain why some assignments behave like copies and others behave like
shared references.

### Stack vs heap

| Type | Allocated on |
|------|-------------|
| `int`, `float`, `bool` | **Stack** — cheap, freed automatically when the function returns |
| `string` | **Heap** — a pointer to a runtime-managed buffer |
| `T[]` (arrays) | **Heap** — a pointer to a growable runtime array |
| Class instances (`new Foo(...)`) | **Heap** — allocated with `jerry_alloc` |
| Function values / closures | **Heap** — a small struct holding a function pointer |

Jerry manages heap memory with **reference counting**. Every heap-allocated
value (string, array, class instance, closure) carries a hidden reference count.
When a variable goes out of scope the compiler emits a `jerry_release` call; when
the count reaches zero the object is freed immediately. There is no garbage
collector and no stop-the-world pause.

String literals passed directly as function arguments are released immediately
after the call returns:

```jerry
doit("hello");   // "hello" is allocated, passed, then freed — no leak
```

**Current limitations** — the following cases do not yet release memory:
- Intermediate string temporaries in expressions (e.g. the `"a"` and `"b"`
  in `"a" + "b" + "c"` before they are consumed by concatenation)
- Reassigning a heap-type variable leaks the old value
- Global variables are not released at program exit (the OS reclaims them)

### Pass by value vs pass by reference

Jerry passes all values as copies of what the variable actually holds.

- **Primitives** (`int`, `float`, `bool`) hold their value directly, so
  passing one to a function copies the value. The caller's variable is
  unaffected.

  ```jerry
  fn double(n: int): int {
      n = n * 2;   // local copy only
      return n;
  }

  fn main() {
      let x: int = 5;
      print(int_to_string(double(x)));  // 10
      print(int_to_string(x));          // 5  — unchanged
  }
  ```

- **Strings, arrays, and class instances** hold a *pointer* to heap memory.
  Passing one to a function copies the pointer — both the caller and the
  callee refer to the same underlying data. Mutations inside the function
  (such as `push` on an array or writing a field on an object) are visible
  to the caller.

  ```jerry
  fn add_item(nums: int[]) {
      push(nums, 99);
  }

  fn main() {
      let xs: int[] = [1, 2, 3];
      add_item(xs);
      print(int_to_string(len(xs)));   // 4 — push was visible to caller
  }
  ```

  Strings are the exception: they are immutable. Every string operation
  (`+`, `string_slice`, etc.) returns a new string rather than mutating
  the original.

### Practical rules of thumb

- Reassigning a variable (`x = ...`) never affects the caller — you're
  changing what the local variable points to, not the data it pointed to.
- Mutating the *contents* of an array or object through a parameter does
  affect the caller's copy.
- Strings are safe to pass freely — they cannot be mutated through any API.

---

## Testing

Jerry includes a built-in test runner and an assertion library in the standard
library. No external tools or frameworks are required.

### Conventions

- Test files are named `*_test.jer`.
- Test functions are named `fn test_*()` — no parameters, no return value.
- Every test file that uses assertions must `include @testing`.

### Writing tests

```jerry
// math_test.jer
include @testing

fn test_addition() {
    assert_eq_int(2 + 2, 4, "addition");
    assert_eq_int(0 + 0, 0, "zero");
}

fn test_signs() {
    assert_true(1 > 0, "positive");
    assert_false(-1 > 0, "negative");
}
```

Assertions do **not** abort on failure — every test function runs to completion,
and the final tally is printed when all tests in the run are done. If any
assertion failed, the process exits with code 1.

### Running tests

```sh
# Run all *_test.jer files in the current directory
jerry test

# Run all tests in a directory
jerry test tests/
jerry test src/

# Run specific test files
jerry test math_test.jer string_test.jer
```

When a directory is given, `jerry test` compiles **all** `.jer` files in that
directory together — not just the test files. This means test files can call
functions defined in the source files alongside them without any extra
configuration. Files that define `fn main()` are automatically excluded to
avoid conflicting with the generated test entry point.

This makes `jerry test src/` the natural way to test a project where source
and tests live together:

```text
src/
├── grep.jer          ← compiled as code under test
├── grep_test.jer     ← test functions discovered and run
└── main.jer          ← excluded (defines fn main)
```

Output on success:

```text
--- src/grep_test.jer ---
  test_no_match
  test_single_match
2 passed
```

Output when a test fails:

```text
--- src/grep_test.jer ---
  test_no_match
  FAIL: expected 0, got 1
  test_single_match
1 passed, 1 failed
```

### Assertion reference

All assertions take a `msg: string` as the final argument — this is printed
alongside the failure to identify which check failed.

| Function | Checks |
|---|---|
| `assert_true(cond: bool, msg)` | `cond` is `true` |
| `assert_false(cond: bool, msg)` | `cond` is `false` |
| `assert_eq_int(a: int, b: int, msg)` | `a == b`, prints values on mismatch |
| `assert_eq_string(a: string, b: string, msg)` | `a == b`, prints values on mismatch |
| `assert_eq_bool(a: bool, b: bool, msg)` | `a == b`, prints values on mismatch |
| `assert_eq_float(a: float, b: float, msg)` | `a == b`, prints values on mismatch |

`test_summary()` is called automatically by `jerry test` — you do not call it
yourself.

### Notes

- A test function that calls `panic` will bring down the whole run; there is no
  per-test isolation. Keep panics out of test code.
- When running against a directory, any source file that defines `fn main()` is
  silently skipped — it would conflict with the generated test entry point.
- Closures cannot capture local variables. If a void callback needs to
  accumulate a result, use a module-level global as a shared accumulator (see
  [`tests/closures_test.jer`](../tests/closures_test.jer) for an example).
- The `tests/` directory in the Jerry repository contains the full test suite
  and is a good reference for patterns.

---

## Built-in functions

These are always available — no `include` required.

### I/O

| Builtin                      | Description                                        |
|------------------------------|----------------------------------------------------|
| `print(x): void`             | Write `x` to stdout followed by a newline          |
| `write(x): void`             | Like `print`, but no trailing newline              |
| `println(): void`            | Write a single newline                             |
| `print_err(s: string): void` | Write `s` to stderr                                |
| `read_stdin(): string`       | Read all of stdin                                  |
| `read_file(path): string`    | Read a file into a string                          |
| `write_file(path, content)`  | Write `content` to `path` (overwrites)             |
| `delete_file(path): void`    | Delete a file                                      |
| `each_line(path, f)`         | Call `f(line)` for each line in a file             |
| `is_dir(path): bool`         | True if `path` is a directory                      |
| `list_dir(path): string[]`   | Return sorted filenames in a directory             |
| `args(): string[]`           | Process argument vector (`args()[0]` is program 0) |

### Conversions

| Builtin                              | Description                          |
|--------------------------------------|--------------------------------------|
| `int_to_string(n: int): string`      | Decimal representation               |
| `float_to_string(f: float): string`  | Decimal representation               |
| `bool_to_string(b: bool): string`    | `"true"` / `"false"` (from core)     |
| `char_to_string(code: int): string`  | One-character string from code point |

### Strings & arrays

| Builtin                                          | Description                          |
|--------------------------------------------------|--------------------------------------|
| `len(s): int`                                    | Length of string or array            |
| `char_at(s: string, i: int): int`                | Code point at index `i`              |
| `string_slice(s: string, start: int, end: int)`  | Substring `s[start:end]`             |
| `string_contains(s, sub): bool`                  | True if `s` contains `sub`           |
| `string_starts_with(s, prefix): bool`            | True if `s` starts with `prefix`     |
| `string_ends_with(s, suffix): bool`              | True if `s` ends with `suffix`       |
| `string_index_of(s, sub): int`                   | First index of `sub` in `s`, or `-1` |
| `string_to_int(s: string): int`                  | Parse a decimal integer string       |
| `string_to_lower(s: string): string`             | ASCII lowercase copy of `s`          |
| `string_to_upper(s: string): string`             | ASCII uppercase copy of `s`          |
| `string_trim(s: string): string`                 | Strip leading/trailing whitespace    |
| `string_replace(s, from, to): string`            | Replace all occurrences of `from`    |
| `read_bytes(n: int): string`                     | Read exactly `n` bytes from stdin    |
| `push(arr, x): void`                             | Append to array                      |

### Maps

| Builtin                        | Description                                   |
|--------------------------------|-----------------------------------------------|
| `map_set(m, key, val): void`   | Insert or overwrite `key → val`               |
| `map_get(m, key): V`           | Retrieve value for `key`                      |
| `map_has(m, key): bool`        | True if `key` is present                      |
| `map_delete(m, key): void`     | Remove `key` (no-op if absent)                |
| `map_len(m): int`              | Number of entries                             |
| `map_keys(m): K[]`             | Array of all keys (unordered)                 |

### Process & environment

| Builtin                         | Description                                      |
|---------------------------------|--------------------------------------------------|
| `exec(args: string[]): int`     | Run a subprocess; returns its exit code          |
| `getenv(name: string): string`  | Value of an environment variable (empty if unset)|
| `exit(code: int): void`         | Terminate the process                            |
| `panic(msg: string): void`      | Abort with an error message                      |

### Time

| Builtin                  | Description                              |
|--------------------------|------------------------------------------|
| `now_millis(): int`      | Unix epoch milliseconds                  |
| `now_seconds(): int`     | Unix epoch seconds                       |
| `now_string(): string`   | Local time as `YYYY-MM-DD HH:MM:SS`      |

---

## The always-available core library

[`stdlib/core.jer`](../stdlib/core.jer) is included automatically in every
Jerry program. It provides small numeric utilities:

```jerry
fn main() {
    print(int_to_string(int_abs(-7)));        // 7
    print(int_to_string(int_max(3, 9)));      // 9
    print(int_to_string(int_min(3, 9)));      // 3

    print(float_to_string(float_abs(-2.5)));  // 2.5
    print(float_to_string(float_max(1.0, 2.0)));
    print(float_to_string(float_min(1.0, 2.0)));

    print(bool_to_string(true));              // true
}
```

---

## Time and the `Timer` class

The stdlib module `@time` exposes a stopwatch and a duration formatter:

```jerry
include @time

fn main() {
    let t: Timer = new Timer();

    // ... do work ...
    let sum: int = 0;
    for (let i: int = 0; i < 1000000; i++) {
        sum = sum + i;
    }

    print("done in " + t.elapsed_string());     // e.g. "42ms"
    print(millis_to_duration(125000));          // "2m05s"
    print(millis_to_duration(3723000));         // "1h02m03s"
}
```

The full source is in [`stdlib/time.jer`](../stdlib/time.jer).

---

## JSON with `@json`

The stdlib module `@json` provides a recursive-descent JSON parser, a
serializer, and helpers for building and querying JSON objects.

```jerry
include @json

fn main() {
    // Parse
    let v: JsonValue = json_parse("{\"name\": \"jerry\", \"version\": 1}");
    print(json_get_string(v, "name"));           // jerry
    print(int_to_string(json_get_int(v, "version")));  // 1

    // Build and serialize
    let resp: JsonValue = json_new_object();
    json_set_string(resp, "status", "ok");
    json_set_bool(resp, "ready", true);
    print(json_stringify(resp));                 // {"status":"ok","ready":true}
}
```

### Kind constants

| Constant      | Value | Meaning          |
|---------------|-------|------------------|
| `JSON_NULL`   | 0     | JSON `null`      |
| `JSON_BOOL`   | 1     | boolean          |
| `JSON_INT`    | 2     | integer number   |
| `JSON_FLOAT`  | 3     | floating-point   |
| `JSON_STRING` | 4     | string           |
| `JSON_ARRAY`  | 5     | array            |
| `JSON_OBJECT` | 6     | object           |

### Key functions

| Function | Description |
|---|---|
| `json_parse(s): JsonValue` | Parse a JSON string |
| `json_stringify(v): string` | Serialize to JSON |
| `json_new_object(): JsonValue` | Create an empty object |
| `json_new_array(): JsonValue` | Create an empty array |
| `json_null_val() / json_bool_val(b) / json_int_val(n) / json_string_val(s)` | Value constructors |
| `json_get_string(obj, key): string` | Get string field |
| `json_get_int(obj, key): int` | Get int field |
| `json_get_bool(obj, key): bool` | Get bool field |
| `json_get_object(obj, key): JsonValue` | Get nested object |
| `json_get_array(obj, key): JsonValue` | Get array field |
| `json_get_val(obj, key): JsonValue` | Get raw value (any type) |
| `json_has_key(obj, key): bool` | Check key existence |
| `json_set_string/int/bool/object/array(obj, key, val)` | Set or overwrite a field |
| `json_set_val(obj, key, val)` | Set raw value |
| `json_array_push_string/int/object(arr, val)` | Append to array |

The full source is in [`stdlib/json.jer`](../stdlib/json.jer).

---

## The `@string` stdlib module

`@string` is bundled with the compiler and provides character classification,
string inspection, splitting, joining, and trimming. No `jerry get` needed.

```jerry
include @string

fn main() {
    // Character classification (operate on code points from char_at)
    print(bool_to_string(is_digit(char_at("9", 0))));    // true
    print(bool_to_string(is_alpha(char_at("A", 0))));    // true
    print(bool_to_string(is_whitespace(char_at(" ", 0)))); // true

    // Splitting and joining
    let parts: string[] = split("one,two,three", ",");
    print(parts[1]);                          // two
    print(join(parts, " | "));               // one | two | three

    // Splitting on whitespace
    let words: string[] = split_whitespace("  the  quick  fox  ");
    print(int_to_string(len(words)));         // 3

    // Trimming and inspection
    print("[" + trim("  hello  ") + "]");    // [hello]
    print(bool_to_string(contains("hello", "ell")));  // true
    print(int_to_string(index_of("hello", "ll")));    // 2
}
```

### `@string` API reference

**Character classification** — take a code point (`int`), return `bool` or `int`.

| Function | Description |
|---|---|
| `is_digit(c)` | `c` is `'0'`–`'9'` |
| `is_alpha(c)` | `c` is `'A'`–`'Z'` or `'a'`–`'z'` |
| `is_alnum(c)` | `is_digit(c) \|\| is_alpha(c)` |
| `is_whitespace(c)` | space / tab / `\n` / `\r` |
| `is_upper(c)` / `is_lower(c)` | ASCII case test |
| `to_lower(c): int` / `to_upper(c): int` | Case-fold a single code point |

**Inspection**

| Function | Returns |
|---|---|
| `starts_with(s, prefix): bool` | `s` begins with `prefix` |
| `ends_with(s, suffix): bool` | `s` ends with `suffix` |
| `index_of(s, needle): int` | Index of first match, or `-1` |
| `contains(s, needle): bool` | `index_of(s, needle) >= 0` |

**Transformation**

| Function | Returns |
|---|---|
| `trim_left(s)` / `trim_right(s)` | Strip leading / trailing whitespace |
| `trim(s)` | `trim_left(trim_right(s))` |
| `repeat(s, n)` | `s` concatenated `n` times |
| `lower_str(s)` | Every ASCII letter lowercased |

**Splitting and joining**

| Function | Returns |
|---|---|
| `split(s, sep): string[]` | All parts; `sep == ""` splits into characters |
| `split_whitespace(s): string[]` | Non-empty whitespace-separated tokens |
| `join(parts, sep): string` | Concatenate with separator |

> **Note:** the [`jerry-string` remote module](#the-jerry-string-remote-module)
> provides the same API. Prefer `include @string` for new projects — it requires
> no `jerry get` and is always in sync with the compiler.

---

## Calling native code with `extern fn`

Jerry can call functions written in C, NASM, or any language that follows the
C ABI. Declare the function with `extern fn` (no body, ends with `;`), then
link the compiled object file or archive when building.

### Declaring extern functions

```jerry
// Declare external functions — types must match the C signature.
extern fn add(a: int, b: int): int;
extern fn greet(): void;

fn main() {
    print(int_to_string(add(3, 4)));   // 7
    greet();
}
```

Jerry `int` maps to C `int64_t`; `float` maps to `double`; `void` is `void`.
The function name is used verbatim — no `_jerry` suffix is added.

### Linking a C library

```c
// hello.c
#include <stdio.h>
#include <stdint.h>

int64_t add(int64_t a, int64_t b) { return a + b; }
void greet(void) { puts("Hello From C"); }
```

```sh
# Compile the C file into an archive
cc -O2 -c hello.c -o hello.o
ar rcs libhello.a hello.o

# Compile the Jerry program, linking against the archive
jerry-compiler main.jer -o myapp -lhello -L.
```

Pass `-lname` to link `libname.a` (or the dynamic equivalent), and `-L/path`
to add a directory to the linker search path. Both the combined form (`-lhello`)
and the separate form (`-l hello`) are accepted.

### Linking a NASM assembly function (macOS / Linux)

```asm
; hello.asm
%ifdef MACHO          ; macOS: symbols carry a _ prefix
    %define FN_NAME _hello_from_nasm
    %define PUTS    _puts
%else                 ; Linux ELF64
    %define FN_NAME hello_from_nasm
    %define PUTS    puts
%endif

section .data
    msg db "Hello From NASM", 0

section .text
    global FN_NAME
    extern PUTS

FN_NAME:
    push rbp
    mov  rbp, rsp
    and  rsp, -16
    lea  rdi, [rel msg]
    call PUTS
    pop  rbp
    ret
```

```sh
# macOS
nasm -f macho64 -DMACHO hello.asm -o hello.o

# Linux
nasm -f elf64 hello.asm -o hello.o
```

See [`examples/linking/`](../examples/linking/) for a complete working project
that calls both C and NASM functions from Jerry, with a `Makefile` that handles
platform detection automatically.

### Passing linker flags through `jerry test`

`jerry test` forwards `-l` and `-L` flags to the compiler, so extern functions
work in test files too:

```sh
jerry test extern_test.jer -lextern_test -L.
```

---

## Modules and remote packages

There are three kinds of code visibility in Jerry:

1. **Core** — `stdlib/core.jer`, always available, never `include`d.
2. **Stdlib** — bundled with the compiler, opt-in via `include @name`.
3. **Remote** — fetched from GitHub, opt-in via `include "github.com/owner/repo"`.

### Including stdlib modules

```jerry
include @time

fn main() {
    print(now_string());
}
```

### Including remote modules

A remote module is referenced by its GitHub path:

```jerry
include "github.com/jeffscottbrown/jerry-string"

fn main() {
    let words: string[] = split_whitespace("the quick brown fox");
    print(words[1]);    // quick
}
```

Before you can compile a file that references a remote module, you must
download and pin it:

```sh
jerry get github.com/jeffscottbrown/jerry-string@v0.0.1
jerry get github.com/jeffscottbrown/jerry-logging@v0.0.1
```

This creates two files in your project directory (next to your `.jer`
sources):

- **`jerry.remotes`** — pinned versions
  ```text
  github.com/jeffscottbrown/jerry-logging v0.0.1
  github.com/jeffscottbrown/jerry-string  v0.0.1
  ```
- **`jerry.sum`** — content hashes for verification
  ```text
  github.com/jeffscottbrown/jerry-logging v0.0.1 h1:e5be59d9...
  github.com/jeffscottbrown/jerry-string  v0.0.1 h1:4a27dd3d...
  ```

Modules are cached at `~/.jerry/cache/remotes/<path>@<version>/`. Run
`jerry sweep` to reconcile `jerry.remotes` and `jerry.sum` after editing
includes.

### Authoring a remote module

When Jerry fetches a remote module it recursively collects every `.jer` file
in the repository, so source files can live anywhere — the root, a `src/`
subdirectory, or any other layout:

```text
my-lib/
├── strings.jer          # included
└── utils.jer            # included

my-lib/
├── src/
│   ├── strings.jer      # included
│   └── utils.jer        # included
└── extras/
    └── advanced.jer     # included
```

**Test files are automatically excluded.** Any file whose name ends in
`_test.jer` is never compiled into a consumer's build, regardless of where it
lives in the repository. This means a library can keep its tests alongside
source or in a dedicated directory without any extra configuration:

```text
my-lib/
├── src/
│   ├── strings.jer          # included by consumers
│   └── strings_test.jer     # excluded from consumers, run with jerry test
└── tests/
    └── integration_test.jer # excluded from consumers, run with jerry test
```

To run the library's own tests locally:

```sh
jerry test src/
jerry test tests/
```

---

## The `jerry-string` remote module

Repository: [`github.com/jeffscottbrown/jerry-string`](https://github.com/jeffscottbrown/jerry-string)

This module gives you the string operations that aren't built in to the
compiler: character classification, slicing-by-content, splitting, joining,
and trimming.

```jerry
include "github.com/jeffscottbrown/jerry-string"

fn main() {
    let s: string = "  Hello, Jerry World!  ";

    // Inspection
    print(bool_to_string(starts_with(trim(s), "Hello")));   // true
    print(bool_to_string(ends_with(trim(s), "World!")));    // true
    print(bool_to_string(contains(s, "Jerry")));            // true
    print(int_to_string(index_of(s, "Jerry")));             // 9

    // Transformation
    print("[" + trim(s) + "]");                             // [Hello, Jerry World!]
    print(repeat("ab", 3));                                 // ababab

    // Splitting and joining
    let parts: string[] = split("one,two,three,four", ",");
    print(parts[2]);                                        // three
    print(join(parts, " | "));                              // one | two | three | four

    let words: string[] = split_whitespace("  the  quick brown  fox  ");
    print(int_to_string(len(words)));                       // 4
    print(words[1]);                                        // quick
}
```

### API reference

**Character classification** — all take a code point (`int`) and return `bool`
unless otherwise noted.

| Function                    | Description                              |
|-----------------------------|------------------------------------------|
| `is_digit(c)`               | `c` is `'0'`–`'9'`                       |
| `is_alpha(c)`               | `c` is `'A'`–`'Z'` or `'a'`–`'z'`        |
| `is_alnum(c)`               | `is_digit(c) || is_alpha(c)`             |
| `is_whitespace(c)`          | space / tab / `\n` / `\r`                |
| `is_upper(c)` / `is_lower(c)` | ASCII case test                        |
| `to_lower(c) / to_upper(c)` | Case-fold a single ASCII code point      |

**Inspection**

| Function                            | Returns                                  |
|-------------------------------------|------------------------------------------|
| `starts_with(s, prefix): bool`      | `s` begins with `prefix`                 |
| `ends_with(s, suffix): bool`        | `s` ends with `suffix`                   |
| `index_of(s, needle): int`          | Index of first match, or `-1`            |
| `contains(s, needle): bool`         | `index_of(s, needle) >= 0`               |

**Transformation**

| Function                       | Returns                                       |
|--------------------------------|-----------------------------------------------|
| `trim_left(s) / trim_right(s)` | `s` with leading/trailing whitespace removed  |
| `trim(s)`                      | `trim_left(trim_right(s))`                    |
| `repeat(s, n)`                 | `s` concatenated `n` times                    |

**Splitting and joining**

| Function                       | Returns                                       |
|--------------------------------|-----------------------------------------------|
| `split(s, sep): string[]`      | All parts. `sep == ""` splits into characters |
| `split_whitespace(s)`          | Non-empty whitespace-separated tokens         |
| `join(parts, sep): string`     | Concatenate with separator                    |

### Worked example: a tiny CSV parser

```jerry
include "github.com/jeffscottbrown/jerry-string"

fn main() {
    let csv: string = "name,age,city\nAlice,30,Austin\nBob,25,Boise";
    let rows: string[] = split(csv, "\n");

    let header: string[] = split(rows[0], ",");
    for (let r: int = 1; r < len(rows); r++) {
        let cells: string[] = split(rows[r], ",");
        for (let c: int = 0; c < len(cells); c++) {
            print(header[c] + " = " + cells[c]);
        }
        print("---");
    }
}
```

---

## The `jerry-logging` remote module

Repository: [`github.com/jeffscottbrown/jerry-logging`](https://github.com/jeffscottbrown/jerry-logging)

A structured, level-filtered logger with a process-wide default logger and a
`Logger` class for per-component instances.

### Levels

| Constant    | Value | Routed to | Notes                          |
|-------------|-------|-----------|--------------------------------|
| `LOG_DEBUG` | 0     | stdout    | Verbose diagnostics            |
| `LOG_INFO`  | 1     | stdout    | Default minimum level          |
| `LOG_WARN`  | 2     | stdout    | Recoverable anomalies          |
| `LOG_ERROR` | 3     | **stderr**| Errors that need attention     |
| `LOG_FATAL` | 4     | **stderr**| Logs then calls `exit(1)`      |

Output format:

```text
YYYY-MM-DD HH:MM:SS [LEVEL] message
YYYY-MM-DD HH:MM:SS [LEVEL] [prefix] message   (with a named Logger)
```

### Quick start — module-level logger

```jerry
include "github.com/jeffscottbrown/jerry-logging"

fn main() {
    log_info("server started on :8080");
    log_warn("config file not found, using defaults");
    log_error("database connection lost");

    // Lower the filter to see debug output:
    log_set_level(LOG_DEBUG);
    log_debug("entering request handler");
}
```

Output (timestamps will vary):

```text
2026-05-17 12:00:00 [INFO] server started on :8080
2026-05-17 12:00:00 [WARN] config file not found, using defaults
2026-05-17 12:00:00 [ERROR] database connection lost
2026-05-17 12:00:00 [DEBUG] entering request handler
```

### Named loggers

For per-component prefixes, instantiate a `Logger` directly. Each instance has
its own minimum level.

```jerry
include "github.com/jeffscottbrown/jerry-logging"

fn main() {
    let db: Logger    = new Logger(LOG_WARN, "db");
    let http: Logger  = new Logger(LOG_DEBUG, "http");

    db.info("connected");            // suppressed — below WARN
    db.warn("slow query: 2.3s");     // 2026-05-17 12:00:00 [WARN] [db] slow query: 2.3s

    http.debug("GET /healthz");      // shown — http level is DEBUG
    http.error("upstream 503");      // routed to stderr

    db.set_level(LOG_DEBUG);
    db.debug("query plan: ...");
}
```

### API reference

**Constants** — `LOG_DEBUG`, `LOG_INFO`, `LOG_WARN`, `LOG_ERROR`, `LOG_FATAL`.

**Module-level functions** (operate on the shared default logger):

| Function                  | Description                              |
|---------------------------|------------------------------------------|
| `log_set_level(level)`    | Set minimum level of the default logger  |
| `log_debug(msg)`          | Log at `LOG_DEBUG`                       |
| `log_info(msg)`           | Log at `LOG_INFO`                        |
| `log_warn(msg)`           | Log at `LOG_WARN`                        |
| `log_error(msg)`          | Log at `LOG_ERROR` (to stderr)           |
| `log_fatal(msg)`          | Log at `LOG_FATAL`, then `exit(1)`       |

**`class Logger`**

| Member                                  | Description                          |
|-----------------------------------------|--------------------------------------|
| `new(min_level: int, prefix: string)`   | Construct; pass `""` to omit prefix  |
| `set_level(level: int)`                 | Change the minimum level             |
| `debug(msg) / info(msg) / warn(msg)`    | Emit at the named level              |
| `error(msg)`                            | Emit at `LOG_ERROR` (to stderr)      |
| `fatal(msg)`                            | Emit at `LOG_FATAL`, then `exit(1)`  |

### Combining logging and timing

`jerry-logging` pairs naturally with the stdlib `Timer`:

```jerry
include @time
include "github.com/jeffscottbrown/jerry-logging"

fn expensive_work() {
    let i: int = 0;
    while i < 1000000 {
        i++;
    }
}

fn main() {
    let t: Timer = new Timer();
    log_info("starting work");

    expensive_work();

    log_info("done in " + t.elapsed_string());
}
```

```text
2026-05-17 12:00:00 [INFO] starting work
2026-05-17 12:00:00 [INFO] done in 7ms
```

---

## Working program: end-to-end example

A small word-frequency counter that pulls in both remote modules.

```jerry
// wordcount.jer
include "github.com/jeffscottbrown/jerry-string"
include "github.com/jeffscottbrown/jerry-logging"

fn count_word(words: string[], target: string): int {
    let n: int = 0;
    for (let i: int = 0; i < len(words); i++) {
        if words[i] == target {
            n++;
        }
    }
    return n;
}

fn main() {
    log_set_level(LOG_DEBUG);

    let text: string =
        "the quick brown fox jumps over the lazy dog the fox sleeps";

    log_debug("input length = " + int_to_string(len(text)));

    let words: string[] = split_whitespace(text);
    log_info("found " + int_to_string(len(words)) + " words");

    let needle: string = "the";
    let count: int = count_word(words, needle);
    log_info("count of '" + needle + "' = " + int_to_string(count));

    if count == 0 {
        log_error("expected at least one match");
    }
}
```

Setup and run:

```sh
jerry get github.com/jeffscottbrown/jerry-string@v0.0.1
jerry get github.com/jeffscottbrown/jerry-logging@v0.0.1
jerry run wordcount.jer
```

```text
2026-05-17 12:00:00 [DEBUG] input length = 58
2026-05-17 12:00:00 [INFO] found 12 words
2026-05-17 12:00:00 [INFO] count of 'the' = 3
```

---

## Showcase: gdgrep

For a complete, production-style Jerry program, take a look at
**[gdgrep](https://github.com/jeffscottbrown/gdgrep)** — a fast, friendly
`grep` replacement written entirely in Jerry.

### What it demonstrates

- **Multi-file projects** — source split across `src/strings.jer`,
  `src/grep.jer`, and `src/main.jer`, all compiled together with a single
  `jerry compile` invocation.
- **CLI argument parsing** — reading `args()` and dispatching on flags
  (`-i` for case-insensitive matching, `-n` for line numbers).
- **File and stdin I/O** — `each_line` over file arguments, `read_stdin` when
  no files are supplied, and proper exit-code handling.
- **String utilities in pure Jerry** — small `to_lower` and `contains`
  implementations.
- **Release engineering** — a GitHub Actions workflow that builds pre-built
  binaries for macOS (arm64, x86_64) and Linux (x86_64), publishes them with
  checksums, and auto-updates a Homebrew tap.

### Installing gdgrep

Because `gdgrep` ships pre-built binaries, end users don't need the Jerry
toolchain installed.

```sh
# Homebrew (macOS and Linux)
brew tap jeffscottbrown/gdgrep
brew install gdgrep

# Or grab a release binary directly
curl -fsSL https://github.com/jeffscottbrown/gdgrep/releases/latest/download/gdgrep-macos-arm64.tar.gz | tar -xz
sudo mv gdgrep-macos-arm64 /usr/local/bin/gdgrep

# Or build from source (requires the Jerry compiler and clang)
git clone https://github.com/jeffscottbrown/gdgrep.git
cd gdgrep && make install
```

### Using it

```sh
# Basic search
gdgrep error app.log

# Case-insensitive, with line numbers
gdgrep -i -n TODO src/main.jer

# Multi-file output (labels with filename:line)
gdgrep -n fn src/strings.jer src/grep.jer src/main.jer

# Pipeline
cat access.log | gdgrep 404
```

### Why read its source?

If you're writing your first Jerry tool, `gdgrep` is the closest thing to a
reference application: it's small (three files), it does something genuinely
useful, and it covers the patterns you'll need for any CLI utility — argument
parsing, file iteration, stdin support, exit codes, and shipping a binary your
users can `brew install`.

---

## CLI reference

| Command                                 | Purpose                                       |
|-----------------------------------------|-----------------------------------------------|
| `jerry run <file.jer> [args...]`        | Compile and execute in one step               |
| `jerry compile <file.jer> -o <bin>`     | Build a native binary                         |
| `jerry ir <file.jer>`                   | Print the generated LLVM IR                   |
| `jerry test [dir or files...]`          | Run unit tests (see [Testing](#testing))      |
| `jerry get <path>@<version>`            | Fetch and pin a remote module                 |
| `jerry sweep`                           | Reconcile `jerry.remotes` and `jerry.sum`     |
| `jerry --version`                       | Print the compiler version                    |

Multi-file projects compile by listing every `.jer` file:

```sh
jerry compile main.jer util.jer -o myapp
```

Project layout for a typical app:

```text
myapp/
├── main.jer
├── util.jer
├── tests/
│   ├── util_test.jer
│   └── core_test.jer
├── jerry.remotes        # pinned remote versions
└── jerry.sum            # content hashes
```

---

## Where to go next

- Browse the runnable examples in [`examples/`](../examples/).
- Read the test suite in [`tests/`](../tests/) — every language feature has
  assertions you can learn from and run with `jerry test tests/`.
- Read **[gdgrep](https://github.com/jeffscottbrown/gdgrep)** — a full,
  install-it-today utility written in Jerry, and a great reference for your
  own projects.
- Read [`stdlib/core.jer`](../stdlib/core.jer) and
  [`stdlib/time.jer`](../stdlib/time.jer) — they're short and they show
  idiomatic Jerry.
- File issues or feature requests on the
  [Jerry GitHub repo](https://github.com/jeffscottbrown/jerry-lang).
