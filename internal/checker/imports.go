package checker

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/parser"
	"github.com/twill-lang/twill/std"
)

// The checker reads one file. That is deliberate and mostly right: a diagnostic
// about a name defined somewhere else is a diagnostic the checker cannot be
// sure of, and it says nothing rather than guessing.
//
// Enums are the exception, and the reason is exhaustiveness. A `match` is
// checked against the list of an enum's cases, so a match on an enum declared
// in another module could not be checked at all -- and matching on an imported
// enum is not an edge case, it is how the ecosystem is written: warp's pipeline
// kinds, weft's marks, spool's version constraints are all declared in one
// module and matched in another. The check silently did nothing in exactly the
// place it was most wanted, and the failure mode was a run-time "no match arm"
// months later.
//
// So an import is followed, for its enum declarations and its top-level `const`
// bindings, and nothing else. Nothing here types a value, resolves a function,
// or reports a diagnostic about the imported file.
//
// `const` is the second exception and it is here for the same reason as the
// first: the check was useless in the place it was asked for. weft's complaint
// (docs/roadmap.md entry 28) is a theme file declaring a palette that an
// importer replaces, and a rule that reads one file catches only the case where
// a library breaks its own promise, which nobody was worried about. A plain
// `import` copies the name into the importing scope and the handle is shared, so
// an importer's `HEX = ...` and `HEX[0] = ...` are both read by every other
// importer; that was measured, not assumed. Collecting the names is enough to
// refuse it, and it costs one more type switch in the walk that was already
// happening.
//
// Reading files is a capability the checker did not have, so it stays out of
// Check: CheckFile is the entry point that has it, Check is unchanged and
// still pure. A file that cannot be read, or that does not parse, is skipped in
// silence -- it is the importing file being checked, not that one, and its own
// problems are reported when it is checked itself.

// CheckFile analyses a program that came from a file, following its imports far
// enough to know what enums and top-level consts they declare. `path` is the
// file's own path, which is what a relative import resolves against.
func CheckFile(prog *ast.Program, path string) []Diagnostic {
	c := newChecker(prog)
	c.loadImports(prog, path, map[string]bool{})
	env := c.prelude(prog)
	for _, s := range prog.Body {
		c.inferStmt(s, env)
	}
	return dedupe(c.diags)
}

// maxImportFiles bounds how far the walk follows a chain of imports. A cycle is
// already stopped by the seen set; this stops a pathological depth from turning
// a check into a directory traversal. It counts files rather than levels, and
// `src/check.tw` counts the same nine with the same comparison: the two walks
// have to stop in the same place or a program exists that one checker refuses
// and the other calls clean. One did -- a chain of nine files, which the Go
// walk followed and the self-hosted walk stopped one short of -- and the
// corpus sweep could not see it, because nothing in the ecosystem imports that
// deep. The name is what it counts; it was `maxImportDepth` against `>`, which
// followed nine files while saying eight.
const maxImportFiles = 9

// importedConst is a `const` this file did not declare: the import path the
// declaring file was named by, as written, and the line inside it. The path is
// carried because a bare line number from another file is the least useful
// thing a diagnostic can print.
type importedConst struct {
	path string
	line int
}

// loadImports registers what the files this program imports declare that this
// file can be judged against: every enum, so a match on one can be checked for
// exhaustiveness, and every top-level `const`, so an assignment through one can
// be refused.
//
// Enums are registered program-wide because that is how the evaluator registers
// variant constructors. Consts are not: a plain import copies names into the
// importing scope, so those go in one flat map, while a namespaced import puts
// them behind the alias and they go in a map of their own. Which of the two a
// name arrives through decides how it must be written to be assigned, so mixing
// them would refuse `HEX = ...` in a file that can only say `theme.HEX`.
func (c *checker) loadImports(prog *ast.Program, path string, seen map[string]bool) {
	for _, st := range prog.Body {
		imp, ok := st.(*ast.Import)
		if !ok {
			continue
		}
		into := c.importedConsts
		if imp.Alias != "" {
			// A namespaced module's record holds everything its own scope ended
			// up with, which includes the names it imported plainly, so the
			// alias's map is filled by the same walk.
			under, already := c.aliasConsts[imp.Alias]
			if !already {
				under = map[string]importedConst{}
				c.aliasConsts[imp.Alias] = under
			}
			into = under
		}
		// A cycle guard per top-level import rather than one for the whole
		// walk. Sharing it made which map a const landed in depend on which
		// import mentioned the file first, and a name's spelling in this file
		// is not something a sibling import gets to decide. The file cap bounds
		// each branch on its own, and parseImport is what keeps a file two
		// branches both reach from being parsed twice.
		branch := map[string]bool{}
		for k := range seen {
			branch[k] = true
		}
		c.collectImport(imp.Path, path, into, branch)
	}
}

// collectImport reads one imported file, registers its enums program-wide and
// its top-level consts into `into`, and follows its own plain imports into the
// same map. A namespaced import inside an imported file is not followed: its
// names arrive under an alias that means nothing in this file.
func (c *checker) collectImport(importPath, from string, into map[string]importedConst, seen map[string]bool) {
	if len(seen) >= maxImportFiles {
		return
	}
	src, key, found := readImport(importPath, from)
	if !found || seen[key] {
		return
	}
	seen[key] = true
	imported, ok := c.parseImport(key, src)
	if !ok {
		return // Its syntax errors are its own file's business.
	}
	for _, s := range imported.Body {
		switch d := s.(type) {
		case *ast.EnumDecl:
			c.registerEnum(d)
		case *ast.Let:
			// First one wins, as everywhere else in this walk: a name two
			// modules both declare is reported against the one that would be
			// found first, and the ambiguity is not this rule's to resolve.
			if d.Const {
				if _, already := into[d.Name]; !already {
					into[d.Name] = importedConst{path: importPath, line: d.Line}
				}
			}
		}
	}
	for _, s := range imported.Body {
		imp, ok := s.(*ast.Import)
		if !ok {
			continue
		}
		// Enums are registered program-wide however they were reached, because
		// that is how the evaluator registers variant constructors, so an
		// aliased import inside an imported file is still followed. Its consts
		// are not this file's to name: they would be written `mid.theme.HEX`
		// here, which no rule below reads, so they go into a map that is
		// dropped.
		next, branch := into, seen
		if imp.Alias != "" {
			next = map[string]importedConst{}
			// And it gets a copy of the seen set, for the same reason the top
			// level does. Sharing it meant a file reached first under an alias
			// was marked visited with its consts thrown away, so a later plain
			// import of that same file collected nothing: `mid.tw` importing
			// `theme.tw` both ways left `HEX = ...` in the importer accepted
			// here and refused by the self-hosted checker, which does not
			// follow an aliased import at all. A copy still stops a cycle,
			// because it carries every file on the way in, including this one.
			branch = map[string]bool{}
			for k := range seen {
				branch[k] = true
			}
		}
		c.collectImport(imp.Path, key, next, branch)
	}
}

// parseImport parses an imported file once per check. Two branches of the walk
// reach the same file often -- every top-level import gets its own cycle guard,
// and an aliased import now gets its own copy of one -- and without the memo
// each arrival re-parses. That is free enough here to have gone unnoticed and
// is not free in the self-hosted checker, where the same walk runs interpreted;
// both memoise, so the two stay the same walk. A file that does not parse is
// remembered as not parsing, so a broken import is read once too.
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

// registerEnum records an enum's cases, whether it was declared in this file or
// reached through an import. A case name that two enums share is ambiguous as a
// bare name, and a match that uses one is left unjudged rather than judged
// against whichever was seen last.
func (c *checker) registerEnum(ed *ast.EnumDecl) { c.registerEnumFrom(ed, false) }

// registerEnumFrom records an enum's cases. `local` marks a declaration in the
// file being checked, which claims its variant names outright: a name is
// ambiguous only between two enums the file merely imported.
//
// Without that, importing any module that happened to declare a variant name
// your own enum also uses turned off exhaustive matching for your enum, with no
// diagnostic. Imports are registered first, so the collision poisoned the local
// declaration rather than the other way round, which is the failure this file's
// header comment was written to prevent.
func (c *checker) registerEnumFrom(ed *ast.EnumDecl, local bool) {
	if _, already := c.enums[ed.Name]; already && !local {
		return
	}
	names := make([]string, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		names = append(names, v.Name)
		owner, dup := c.variantOwner[v.Name]
		switch {
		case local:
			c.variantOwner[v.Name] = ed.Name
		case dup && owner != ed.Name:
			c.variantOwner[v.Name] = ""
		default:
			c.variantOwner[v.Name] = ed.Name
		}
	}
	c.enums[ed.Name] = names
}

// readImport resolves an import path to its source, returning a key that
// identifies it for the cycle guard. It mirrors the interpreter's resolution:
// a `std/` path names a module compiled into the binary, and anything else is a
// file, relative to the importing file first and then the working directory.
func readImport(importPath, fromFile string) (src, key string, ok bool) {
	if name, isStd := strings.CutPrefix(importPath, "std/"); isStd {
		text, found := std.Read(name)
		return text, "std/" + name, found
	}
	var candidates []string
	if fromFile != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(fromFile), importPath))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, importPath))
	}
	for _, cand := range candidates {
		data, err := os.ReadFile(cand)
		if err != nil {
			continue
		}
		abs, absErr := filepath.Abs(cand)
		if absErr != nil {
			abs = cand
		}
		return string(data), abs, true
	}
	return "", "", false
}
