package main

// Canonical dump mode. `twill run <file> --dump=canonical` runs a program and
// then prints every top-level binding it defined, in a form built to be
// compared byte for byte between two binaries rather than to be read by a
// person.
//
// Floats print as hexadecimal bit patterns because the point of the dump is bit
// equality: the ordinary decimal formatting rounds to six places and would hide
// exactly the one-ulp differences a value-representation change introduces. The
// cost is that the output is unpleasant to read, which is the right trade for a
// file only a differ consumes.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"

	"github.com/twill-lang/twill/internal/checker"
	"github.com/twill-lang/twill/internal/interp"
	"github.com/twill-lang/twill/internal/parser"
	"github.com/twill-lang/twill/internal/tensor"
	"github.com/twill-lang/twill/internal/value"
)

// canonicalDumpFlag is spelled as one token so that adding it cannot change how
// any existing argument is parsed.
const canonicalDumpFlag = "--dump=canonical"

// dumpDepthLimit stops a self-referential record or list from recursing
// forever. A program that nests deeper than this loses the tail of one value,
// which is preferable to the dumper hanging on the corpus.
const dumpDepthLimit = 32

// runFileCanonical mirrors runFile and then dumps. It is a separate function
// rather than a flag inside runFile so that the existing path keeps exactly the
// behaviour it had.
func runFileCanonical(path string, check bool) int {
	if err := interp.CheckLegacyExt(path); err != nil {
		fmt.Fprintf(os.Stderr, "twill: %s\n", err)
		return 1
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "twill: cannot read file %q\n", path)
		return 1
	}

	if check {
		prog, perr := parser.Parse(string(src))
		if perr != nil {
			reportError(path, string(src), perr)
			return 1
		}
		diags := checker.CheckFile(prog, path)
		if len(diags) > 0 {
			printDiags(path, string(src), diags)
			// Only an error refuses. A warning describes the program the author
			// wrote and is printed above; the program still runs.
			if n := checker.CountErrors(diags); n > 0 {
				fmt.Fprintf(os.Stderr, "twill: %d shape error(s); not running (use --no-check to run anyway)\n", n)
				return 1
			}
		}
	}

	ip := interp.New(nil)

	// The builtins live in the same scope as the program's own bindings, so the
	// only way to tell them apart is to note which names existed before the
	// program ran.
	predefined := map[string]bool{}
	for name := range ip.Global.Locals() {
		predefined[name] = true
	}

	// RunFile resolves imports and file IO against the script's directory but
	// throws the program's final value away, and that value is the whole
	// content of most fixtures: an expression like `1 + 2 * 3` binds nothing.
	// Changing the working directory reproduces RunFile's path resolution
	// without touching the interpreter. The cost is a process-global change,
	// which is acceptable only because a dump run does one file and exits.
	if dir := filepath.Dir(path); dir != "" {
		if err := os.Chdir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "twill: cannot enter %q\n", dir)
			return 1
		}
	}
	result, err := ip.Run(string(src))
	if err != nil {
		reportError(filepath.Base(path), string(src), err)
		return 1
	}

	locals := ip.Global.Locals()
	names := make([]string, 0, len(locals))
	for name := range locals {
		if !predefined[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	fmt.Println("# canonical v1")
	// The program's final value comes first, under a name no Twill program can
	// bind, because for most fixtures it is the only thing they produce.
	fmt.Printf("_result = %s\n", canonical(result, 0))
	for _, name := range names {
		fmt.Printf("%s = %s\n", name, canonical(locals[name], 0))
	}
	return 0
}

// canonical renders one value. A number and a rank-0 tensor render the same
// way on purpose: in Twill they are the same value, and which of the two
// representations the interpreter happened to use is the detail this dump must
// not pin, or every unboxing change would read as a divergence.
func canonical(v value.Value, depth int) string {
	if depth > dumpDepthLimit {
		return "elided"
	}
	switch t := v.(type) {
	case *tensor.Tensor:
		if t.IsScalar() {
			return "num " + hexFloat(t.Data[0])
		}
		out := "tensor shape=[" + joinInts(t.Shape) + "] data=["
		for i, x := range t.Data {
			if i > 0 {
				out += " "
			}
			out += hexFloat(x)
		}
		return out + "]"
	case value.Bool:
		return "bool " + strconv.FormatBool(bool(t))
	case value.Str:
		return "str " + strconv.Quote(string(t))
	case value.Unit:
		return "unit"
	case *value.List:
		out := "list ["
		for i, item := range t.Items {
			if i > 0 {
				out += ", "
			}
			out += canonical(item, depth+1)
		}
		return out + "]"
	case *value.Tuple:
		out := "tuple ("
		for i, item := range t.Items {
			if i > 0 {
				out += ", "
			}
			out += canonical(item, depth+1)
		}
		return out + ")"
	case *value.Record:
		keys := make([]string, len(t.Keys))
		copy(keys, t.Keys)
		sort.Strings(keys)
		out := "record {"
		for i, k := range keys {
			if i > 0 {
				out += ", "
			}
			out += k + ": " + canonical(t.Fields[k], depth+1)
		}
		return out + "}"
	case *value.Closure:
		name := t.Name
		if name == "" {
			name = "anonymous"
		}
		return fmt.Sprintf("fn %s/%d", name, len(t.Params))
	case *value.Builtin:
		return "builtin " + t.Name
	case nil:
		return "nil"
	}

	// An unboxed scalar is recognised by its kind rather than by naming its
	// type, so that this file compiles against builds either side of the
	// unboxing change. Naming value.Num would make the dumper unbuildable
	// against the very reference binary it has to be compared with.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Float64 {
		return "num " + hexFloat(rv.Float())
	}
	// Opaque natives (a fitted model, say) have no portable form, so the dump
	// records that one is bound here and leaves its contents to the round-trip
	// checks instead.
	return fmt.Sprintf("opaque %T", v)
}

// hexFloat prints the exact bits. NaN collapses to one spelling, so a dump
// cannot distinguish two NaN payloads; no Twill operation makes that
// distinction observable, and the alternative reads far worse.
func hexFloat(x float64) string {
	return strconv.FormatFloat(x, 'x', -1, 64)
}

func joinInts(xs []int) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += strconv.Itoa(x)
	}
	return out
}
