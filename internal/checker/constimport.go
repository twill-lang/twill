package checker

import (
	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/parser"
)

// `const` across a file boundary.
//
// The checker reads one file, and imports.go says why that is mostly right.
// `const` is the second exception, after enums, and it is here for the same
// reason: the rule was useless in the place it was asked for. weft's complaint
// (docs/roadmap.md entry 28) is a theme file declaring a palette that an
// importer replaces. A rule that reads one file catches a library breaking its
// own promise inside its own file, which nobody was worried about. A plain
// `import` copies the name into the importing scope and the handle is shared,
// so an importer's `HEX = ...` and `HEX[0] = ...` are both what every other
// importer then reads; under an alias the same two writes are spelled
// `theme.HEX = ...` and `theme.HEX[0] = ...`. Measured on both evaluators, not
// assumed.
//
// This walk is its own walk. A first attempt rode on the enum walk in
// imports.go and broke it: changing how that walk bounded and guarded itself
// stopped a file with nine or more siblings being followed to the end, so a
// non-exhaustive match was accepted depending on the order the imports were
// written in. So loadImportedEnums is untouched, and this file follows imports
// again, for top-level `const` bindings and nothing else. Two walks over the
// same files cost a second parse each, which is nothing here and is memoised
// anyway.
//
// Nothing here types a value, resolves a function, or reports a diagnostic
// about the imported file. A file that cannot be read, or that does not parse,
// is skipped in silence: its own problems are reported when it is checked.

// maxConstImportDepth bounds how many levels of import the const walk follows.
// A cycle is already stopped by the seen set; this stops a pathological chain
// from turning a check into a directory traversal. It counts levels, so how
// many siblings a file imports never changes whether one of them is followed.
// `src/check.tw` counts the same eight, and the two walks have to stop in the
// same place or a program exists that one checker refuses and the other calls
// clean.
const maxConstImportDepth = 8

// importedConst is a `const` this file did not declare: the import path the
// declaring file was named by, as written, and the line inside it. The path is
// carried because a bare line number from another file is the least useful
// thing a diagnostic can print.
type importedConst struct {
	path string
	line int
}

// loadImportedConsts fills importedConsts from every plain top-level import,
// and aliasConsts from every namespaced one, keyed by alias. Which of the two a
// name arrives through decides how it must be written to be assigned, so they
// are kept apart: mixing them would refuse `HEX = ...` in a file that can only
// say `theme.HEX`.
//
// Each top-level import walks under its own seen set. A file reached through
// two of them lands in both maps, which is what the evaluator does with it.
func (c *checker) loadImportedConsts(prog *ast.Program, path string) {
	for _, st := range prog.Body {
		imp, ok := st.(*ast.Import)
		if !ok {
			continue
		}
		into := c.importedConsts
		if imp.Alias != "" {
			under, already := c.aliasConsts[imp.Alias]
			if !already {
				under = map[string]importedConst{}
				c.aliasConsts[imp.Alias] = under
			}
			into = under
		}
		c.collectConsts(imp.Path, path, into, map[string]bool{}, 0)
	}
}

// collectConsts reads one imported file, records its top-level consts in
// `into`, and follows its own plain imports into the same map. A namespaced
// import inside an imported file is not followed: its names would be written
// `mid.theme.HEX` here, which no rule reads.
func (c *checker) collectConsts(importPath, from string, into map[string]importedConst, seen map[string]bool, depth int) {
	if depth >= maxConstImportDepth {
		return
	}
	src, key, found := readImport(importPath, from)
	if !found || seen[key] {
		return
	}
	seen[key] = true
	imported, ok := c.parseImport(key, src)
	if !ok {
		return
	}
	for _, s := range imported.Body {
		lt, isLet := s.(*ast.Let)
		if !isLet || !lt.Const {
			continue
		}
		// First one wins: a name two modules both declare is reported against
		// the one found first, and the ambiguity is not this rule's to resolve.
		if _, already := into[lt.Name]; !already {
			into[lt.Name] = importedConst{path: importPath, line: lt.Line}
		}
	}
	for _, s := range imported.Body {
		if imp, isImp := s.(*ast.Import); isImp && imp.Alias == "" {
			c.collectConsts(imp.Path, key, into, seen, depth+1)
		}
	}
}

// parseImport parses an imported file once per check, by resolved path. A file
// that does not parse is remembered as not parsing, so a broken import is read
// once too.
func (c *checker) parseImport(key, src string) (*ast.Program, bool) {
	if prog, cached := c.parsedImports[key]; cached {
		return prog, prog != nil
	}
	prog, err := parser.Parse(src)
	if err != nil {
		c.parsedImports[key] = nil
		return nil, false
	}
	c.parsedImports[key] = prog
	return prog, true
}

// bindsAtRoot reports whether a name reaches the file's own top-level scope
// rather than a nearer one. A name nothing binds counts as reaching it: that is
// what a plain import's names look like to this checker, which records the
// imported file's consts without defining them as values.
func (e *checkEnv) bindsAtRoot(name string) bool {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.vars[name]; ok {
			return env.parent == nil
		}
	}
	return true
}

// lvaluePath is lvalueBase plus the field name nearest the base: `theme`, `HEX`
// for `theme.HEX[0]`. That pair is what an assignment through a namespaced
// import looks like, and the field is empty when the target reaches the base
// without one.
func lvaluePath(target ast.Expr) (base *ast.Ident, field string, ok bool) {
	for {
		switch t := target.(type) {
		case *ast.Ident:
			return t, field, true
		case *ast.Field:
			field = t.Name
			target = t.Target
		case *ast.Index:
			target = t.Target
		default:
			return nil, "", false
		}
	}
}

// importedConstFor reports the `const` an assignment target reaches in another
// file, if it reaches one, with the name it is known by here. A plain import
// puts the name in this scope, so `HEX` and `HEX[0]` reach it directly; a
// namespaced import puts it behind the alias, so `theme.HEX` and `theme.HEX[0]`
// do.
//
// A nearer binding wins in both shapes. A parameter or a local `let` named
// after an imported const is a different binding in a different scope, and
// writing to it has nothing to do with the imported table.
func (c *checker) importedConstFor(target ast.Expr, env *checkEnv) (string, importedConst, bool) {
	base, field, ok := lvaluePath(target)
	if !ok || !env.bindsAtRoot(base.Name) {
		return "", importedConst{}, false
	}
	if field != "" {
		if under, isAlias := c.aliasConsts[base.Name]; isAlias {
			ic, isConst := under[field]
			return field, ic, isConst
		}
	}
	ic, isConst := c.importedConsts[base.Name]
	return base.Name, ic, isConst
}

// reportImportedConstRebinds refuses a top-level binding of a name a plain
// import brought in as a const. That is reportConstRebinds reaching across the
// file boundary: the importer's `let HEX` is what a second importer's function
// then reads. A destructuring `let` is a binding too, and is counted.
func (c *checker) reportImportedConstRebinds(body []ast.Stmt) {
	if len(c.importedConsts) == 0 {
		return
	}
	rebind := func(name string, line int) {
		if ic, isConst := c.importedConsts[name]; isConst {
			c.report(line, "%s is declared const in %q on line %d, so this file may not bind the name again: a plain import brings %s into this scope, and a second binding is what every other importer then reads. Rename this binding.", name, ic.path, ic.line, name)
		}
	}
	for _, s := range body {
		switch b := s.(type) {
		case *ast.Let:
			rebind(b.Name, b.Line)
		case *ast.LetTuple:
			for _, name := range b.Names {
				if name != "_" {
					rebind(name, b.Line)
				}
			}
		}
	}
}
