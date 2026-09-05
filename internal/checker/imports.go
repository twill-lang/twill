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
// So an import is followed, for its enum declarations and nothing else. Nothing
// here types a value, resolves a function, or reports a diagnostic about the
// imported file: it collects `enum` declarations, which is the smallest thing
// that makes a cross-module match checkable, and it is why this is fifty lines
// rather than a module system.
//
// Reading files is a capability the checker did not have, so it stays out of
// Check: CheckFile is the entry point that has it, Check is unchanged and
// still pure. A file that cannot be read, or that does not parse, is skipped in
// silence -- it is the importing file being checked, not that one, and its own
// problems are reported when it is checked itself.

// CheckFile analyses a program that came from a file, following its imports far
// enough to know what enums they declare. `path` is the file's own path, which
// is what a relative import resolves against.
func CheckFile(prog *ast.Program, path string) []Diagnostic {
	c := newChecker(prog)
	c.loadImportedEnums(prog, path, map[string]bool{})
	env := c.prelude(prog)
	for _, s := range prog.Body {
		c.inferStmt(s, env)
	}
	return dedupe(c.diags)
}

// maxImportDepth bounds how far the enum walk follows a chain of imports. A
// cycle is already stopped by the seen set; this stops a pathological depth
// from turning a check into a directory traversal.
const maxImportDepth = 8

// loadImportedEnums registers every enum declared in the files this program
// imports, and in the files those import, so that a match on any of them can be
// checked for exhaustiveness.
func (c *checker) loadImportedEnums(prog *ast.Program, path string, seen map[string]bool) {
	if len(seen) > maxImportDepth {
		return
	}
	for _, st := range prog.Body {
		imp, ok := st.(*ast.Import)
		if !ok {
			continue
		}
		src, key, found := readImport(imp.Path, path)
		if !found || seen[key] {
			continue
		}
		seen[key] = true
		imported, err := parser.Parse(src)
		if err != nil {
			continue // Its syntax errors are its own file's business.
		}
		for _, s := range imported.Body {
			ed, isEnum := s.(*ast.EnumDecl)
			if !isEnum {
				continue
			}
			c.registerEnum(ed)
		}
		c.loadImportedEnums(imported, key, seen)
	}
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
