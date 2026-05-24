# Contributing to Jerry

This document covers workflows that come up when changing the compiler,
grammar, or editor extension.

---

## Repository layout

| Repository | Purpose |
|---|---|
| `jerry-lang` | Compiler, runtime, LSP, stdlib, and tree-sitter grammar |
| `jerry-zed` | Zed editor extension (syntax highlighting, LSP wiring) |

The tree-sitter grammar lives in `jerry-lang/tree-sitter-jerry/`. The
`jerry-zed` extension references it by commit hash via the `path` field in
`extension.toml`.

---

## The PEG grammar (`grammar/jerry.peg`)

### Role

`grammar/jerry.peg` is the **canonical specification** of Jerry's syntax.
It is the single document you should read when you need to know exactly what
is or is not legal Jerry.

The file is written in a standard PEG (Parsing Expression Grammar) notation:

| Notation | Meaning |
|---|---|
| `lowercase` | Parser rule — whitespace and comments are skipped between atoms |
| `UPPERCASE` | Lexer rule — atomic; no implicit skipping inside |
| `"..."` | Literal string terminal |
| `[a-z]` | Character class |
| `x?` `x*` `x+` | Optional, zero-or-more, one-or-more |
| `x \| y` | Ordered choice — `x` tried first |
| `!x` `&x` | Negative / positive lookahead |

### Relationship to the compiler

The compiler's lexer (`self-host/lexer.jer`) and parser
(`self-host/parser.jer`) are hand-written recursive-descent implementations
of the same grammar.  They are **not generated from** `jerry.peg` — they are
maintained in parallel.  The grammar does not drive the compiler at runtime.

**Why hand-written?**  A PEG interpreter that runs at compile time would be
significantly slower, and hand-written parsers produce much better error
messages ("expected `;` after statement" rather than "unexpected input at
3:12").

### Conformance enforcement — `jerry parse`

`jerry parse <file.jer>` validates any `.jer` file against the PEG grammar
at runtime, exiting non-zero if the file does not match:

```sh
jerry parse examples/hello.jer          # OK: examples/hello.jer
jerry parse src/main.jer src/util.jer   # validates multiple files
```

CI runs `jerry parse` against representative source files on every push.
This catches cases where the hand-written parser and the grammar diverge —
one accepts something the other rejects.

If you add a new syntax construct:

1. Update `grammar/jerry.peg` first (this is the spec).
2. Implement the change in `self-host/lexer.jer` and/or `self-host/parser.jer`.
3. Add a test `.jer` file that exercises the new construct.
4. Run `jerry parse` against the new file to confirm the grammar matches.

### Keyword list

The `KEYWORD` rule in the grammar is the authoritative list of reserved
words.  The same list is replicated in the lexer (`self-host/lexer.jer`).
When adding or removing a keyword, update **both** files and confirm that
`jerry parse` still accepts all existing test files.

### What the grammar does NOT cover

- **Semantics** — type checking, scoping, and name resolution are defined by
  the type-checker (`self-host/checker.jer`), not the grammar.
- **Standard library** — stdlib modules live in `stdlib/` and are documented
  separately.
- **Tree-sitter grammar** — `tree-sitter-jerry/grammar.js` is a separate
  grammar used for editor syntax highlighting.  It must be kept in sync with
  `jerry.peg` manually (see the tree-sitter section below).

---

## Updating the tree-sitter grammar

### When to update

Update the grammar whenever the Jerry syntax changes — new keywords, new
statement forms, changed expression rules, etc. The grammar drives:

- Syntax highlighting in Zed (and any other tree-sitter-aware editor)
- The `runnables.scm` query that shows the **Run ▶** button next to `fn main()`
- Indentation and bracket-matching queries in jerry-zed

The key file is `tree-sitter-jerry/grammar.js`. The generated files
(`src/grammar.json`, `src/node-types.json`, `src/parser.c`) are committed to
the repo and must be regenerated whenever `grammar.js` changes.

### How to update

1. **Edit `grammar.js`** in `tree-sitter-jerry/`:

   ```sh
   cd tree-sitter-jerry
   # edit grammar.js
   ```

2. **Regenerate the parser** (requires `tree-sitter-cli`):

   ```sh
   npx tree-sitter generate
   ```

   This updates `src/grammar.json`, `src/node-types.json`, and `src/parser.c`.

3. **Test the grammar** against a Jerry source file:

   ```sh
   npx tree-sitter parse ../examples/closures.jer
   ```

   The output should be a clean tree with no `ERROR` or `MISSING` nodes.

4. **Commit everything** — `grammar.js` and all generated files — to `jerry-lang`:

   ```sh
   cd ..   # back to jerry-lang root
   git add tree-sitter-jerry/
   git commit -m "tree-sitter: <describe the syntax change>"
   git push
   ```

5. **Note the new commit hash:**

   ```sh
   git rev-parse HEAD
   ```

   You will need this in the next step.

---

## Updating jerry-zed after a grammar change

`jerry-zed/extension.toml` pins the grammar to a specific commit in
`jerry-lang`. Any time you push a grammar change to `jerry-lang`, you must
update this pin before Zed will use the new grammar.

### Steps

1. **Update `extension.toml`** in `jerry-zed`:

   ```toml
   [grammars.jerry]
   repository = "https://github.com/jeffscottbrown/jerry-lang"
   commit = "<new commit hash from jerry-lang>"
   path = "tree-sitter-jerry"
   ```

2. **Rebuild the extension wasm:**

   ```sh
   cd jerry-zed
   cargo build --release --target wasm32-wasip1
   cp target/wasm32-wasip1/release/jerry_zed.wasm extension.wasm
   ```

3. **Commit and push:**

   ```sh
   git add extension.toml extension.wasm
   git commit -m "bump grammar to <short hash>"
   git push
   ```

4. **Restart Zed** to pick up the new grammar from the symlinked extension.

### Verifying the update

Open a `.jer` file in Zed. New syntax constructs should highlight correctly.
If the Run ▶ button next to `fn main()` disappears, check
`languages/jerry/runnables.scm` — the node type names may have changed in the
new grammar and the query needs updating.

---

## Updating jerry-zed for LSP changes

The LSP is part of the `jerry` binary. `jerry-zed` doesn't pin the LSP to a
commit — it runs whichever `jerry` binary is on the user's `PATH`. No
extension changes are needed for LSP-only fixes or features; users just need
to upgrade their `jerry` binary.

The only time jerry-zed needs a change for LSP work is if:

- The `jerry lsp` command is renamed or its arguments change (`src/lib.rs`)
- A new Zed LSP capability needs to be wired up in `src/lib.rs`

---

## Quick reference

| Changed | Action required |
|---|---|
| `grammar/jerry.peg` | Update lexer/parser in `self-host/` → run `jerry parse` on test files |
| `self-host/lexer.jer` or `self-host/parser.jer` | Sync `grammar/jerry.peg` → run `jerry parse` on test files |
| `grammar.js` | Regenerate parser → commit to jerry-lang → bump commit hash in jerry-zed → rebuild wasm |
| `runnables.scm` / `highlights.scm` / etc. | Commit to jerry-zed → rebuild wasm |
| LSP behaviour (completions, diagnostics, hover) | Commit to jerry-lang only |
| `jerry lsp` CLI interface | Update `jerry-zed/src/lib.rs` → rebuild wasm |
