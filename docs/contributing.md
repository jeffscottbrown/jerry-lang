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
| `grammar.js` | Regenerate parser → commit to jerry-lang → bump commit hash in jerry-zed → rebuild wasm |
| `runnables.scm` / `highlights.scm` / etc. | Commit to jerry-zed → rebuild wasm |
| LSP behaviour (completions, diagnostics, hover) | Commit to jerry-lang only |
| `jerry lsp` CLI interface | Update `jerry-zed/src/lib.rs` → rebuild wasm |
