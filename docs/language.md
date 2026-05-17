# The Jerry Language Guide

Jerry is a small, statically-typed, JavaScript-flavoured language that compiles
to native binaries through LLVM IR. The compiler is written in Go and ships as
a single `jerry` CLI.

This guide is a practical tour: every section is short, every snippet is
runnable. Save any snippet as `foo.jer` and run it with:

```sh
jerry run foo.jer
```

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
9. [Strings](#strings)
10. [Classes](#classes)
11. [Built-in functions](#built-in-functions)
12. [The always-available core library](#the-always-available-core-library)
13. [Time and the `Timer` class](#time-and-the-timer-class)
14. [Modules and remote packages](#modules-and-remote-packages)
15. [The `jerry-string` remote module](#the-jerry-string-remote-module)
16. [The `jerry-logging` remote module](#the-jerry-logging-remote-module)
17. [Working program: end-to-end example](#working-program-end-to-end-example)
18. [CLI reference](#cli-reference)

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

## Strings

Strings are UTF-8 and indexed by code-point position via `char_at`. They are
**immutable** — every transformation returns a new string.

```jerry
fn main() {
    let s: string = "Hello, Jerry!";

    print("len = " + int_to_string(len(s)));               // 13
    print("char_at(0) = " + int_to_string(char_at(s, 0))); // 72  ('H')
    print("char_to_string(72) = " + char_to_string(72));   // H
    print("slice = " + string_slice(s, 0, 5));             // Hello
}
```

For higher-level operations (split, join, trim, starts/ends_with, contains,
case-folding, …) reach for the remote
[`jerry-string`](#the-jerry-string-remote-module) module.

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
| `each_line(path, f)`         | Call `f(line)` for each line in a file             |
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
| `push(arr, x): void`                             | Append to array                      |

### Time

| Builtin                  | Description                              |
|--------------------------|------------------------------------------|
| `now_millis(): int`      | Unix epoch milliseconds                  |
| `now_seconds(): int`     | Unix epoch seconds                       |
| `now_string(): string`   | Local time as `YYYY-MM-DD HH:MM:SS`      |

### Process

| Builtin                | Description                            |
|------------------------|----------------------------------------|
| `exit(code: int): void`| Terminate the process                  |
| `panic(msg: string): void` | Abort with an error message        |

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

## CLI reference

| Command                                 | Purpose                                       |
|-----------------------------------------|-----------------------------------------------|
| `jerry run <file.jer> [args...]`        | Compile and execute in one step               |
| `jerry compile <file.jer> -o <bin>`     | Build a native binary                         |
| `jerry ir <file.jer>`                   | Print the generated LLVM IR                   |
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
├── jerry.remotes        # pinned remote versions
└── jerry.sum            # content hashes
```

---

## Where to go next

- Browse the runnable examples in [`examples/`](../examples/).
- Read [`stdlib/core.jer`](../stdlib/core.jer) and
  [`stdlib/time.jer`](../stdlib/time.jer) — they're short and they show
  idiomatic Jerry.
- File issues or feature requests on the
  [Jerry GitHub repo](https://github.com/jeffscottbrown/jerry-lang).
