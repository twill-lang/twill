# Twill for VS Code

Language support for [Twill](https://github.com/twill-lang/twill), a tensor-first
differentiable language where automatic differentiation and physical units are
built into the language rather than a library.

## Features

- **Syntax highlighting** for `.tw` files: comments, strings (with `\x`/`\u`/`\U`
  escapes), numbers (decimal, hex, scientific), operators (including `@`, `->`,
  `=>`, postfix `?`), and the `mode` declaration.
- **Keyword and type awareness**: control flow (`if`/`else`/`while`/`for`/`match`/
  `break`/`continue`/`return`), declarations (`let`/`const`/`fn`/`enum`/`struct`/
  `type`/`unit`), imports, logical and bitwise operators, and the built-in types
  (`I64`, `F64`, `Bool`, `Str`, `Arr`, `Dict`, `Opt`, `Res`, `Tensor`, …) and
  dtype names (`f32`, `bf16`, `f16`, …).
- **Built-in and autodiff functions** highlighted distinctly: `grad`, `grads`,
  `jacobian`, `hessian`, plus the tensor/array standard library
  (`matmul`, `softmax`, `reshape`, `concat`, `gbm_fit`, …).
- **Variant constructors** (`Ok`, `Err`, `Some`, `None`) and constants
  (`true`, `false`, `unit`).
- **Snippets** for functions, `let`, loops, `if`/`else`, `match`, `enum`,
  `struct`, `import`, `mode systems`, and `grad`.
- **Editor behavior**: line-comment toggling (`#`), bracket matching,
  auto-closing pairs, indentation rules, and `# region` / `# endregion` folding.

## Install

Install **Twill** from the Visual Studio Marketplace, or from a `.vsix`:

```bash
code --install-extension twill-lang-1.0.0.vsix
```

## Build from source

```bash
cd editors/vscode
npx @vscode/vsce package     # produces twill-lang-<version>.vsix
```

## What's here

- `syntaxes/twill.tmLanguage.json` — the TextMate grammar.
- `language-configuration.json` — comments, brackets, indentation, folding.
- `snippets/twill.json` — code snippets.
- `package.json` — the extension manifest.

## License

MIT. See [LICENSE](./LICENSE).
