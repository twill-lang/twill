// Command twill runs Twill programs, checks them, or starts a REPL.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/twill-lang/twill/internal/checker"
	"github.com/twill-lang/twill/internal/format"
	"github.com/twill-lang/twill/internal/interp"
	"github.com/twill-lang/twill/internal/lexer"
	"github.com/twill-lang/twill/internal/parser"
	"github.com/twill-lang/twill/internal/value"
)

const version = "1.11.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		repl()
		return
	}

	switch args[0] {
	case "-v", "--version", "version":
		if hasFlag(args, "--verbose") {
			versionVerbose()
			return
		}
		fmt.Println("Twill", version)
	case "-h", "--help", "help":
		usage()
	case "check":
		os.Exit(checkPaths(sourceArgs(args, "check <path ...>", checkFlags)))
	case "fmt":
		mode := fmtPrint
		switch {
		case hasFlag(args, "--check"):
			// --check and --write are opposite answers to "what should happen to
			// a file that is not formatted", so asking for both is a mistake
			// rather than a precedence question.
			if hasFlag(args, "--write") || hasFlag(args, "-w") {
				fmt.Fprintln(os.Stderr, "twill: fmt --check and --write do opposite things; pick one")
				os.Exit(2)
			}
			mode = fmtCheck
		case hasFlag(args, "--write") || hasFlag(args, "-w"):
			mode = fmtWrite
		}
		os.Exit(formatPaths(os.Stdout, sourceArgs(args, "fmt <path ...> [--write | --check]", fmtFlags), mode))
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: twill run <file>")
			os.Exit(2)
		}
		if hasFlag(args, canonicalDumpFlag) {
			os.Exit(runFileCanonical(args[1], !hasFlag(args, "--no-check")))
		}
		os.Exit(runFile(args[1], !hasFlag(args, "--no-check")))
	case "test":
		os.Exit(runTests(nonFlagArgs(args[1:]), hasFlag(args, "--verbose") || hasFlag(args, "-v"),
			flagValue(args, "--filter")))
	case "doctor":
		os.Exit(doctor())
	case "lsp":
		os.Exit(runLSP())
	case "repl":
		repl()
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(os.Stderr, "twill: unknown flag %q\n", args[0])
			os.Exit(2)
		}
		// A bare word that is not a command, has no extension, has no separator
		// in it and names nothing on disk is a misspelled command. Running it
		// reported `cannot read file "chekc"`, which reads as a missing file and
		// sends the reader looking for one.
		if !looksLikeAPath(args[0]) {
			fmt.Fprintf(os.Stderr, "twill: unknown command %q\n", args[0])
			fmt.Fprintln(os.Stderr, "run 'twill help' for the list of commands")
			os.Exit(2)
		}
		if hasFlag(args, canonicalDumpFlag) {
			os.Exit(runFileCanonical(args[0], !hasFlag(args, "--no-check")))
		}
		os.Exit(runFile(args[0], !hasFlag(args, "--no-check")))
	}
}

func runFile(path string, check bool) int {
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
	result, ranMain, err := ip.RunFileMain(path, scriptArgs(path))
	if err != nil {
		// exit(n) is a status, not a fault, so it carries its own code and prints
		// nothing on the way out.
		var ex *interp.ExitError
		if errors.As(err, &ex) {
			return ex.Code
		}
		reportError(path, string(src), err)
		return 1
	}
	// A systems-mode program's main returns its exit code; a numeric-mode file
	// has no main and its top-level value is not an exit code, so it exits 0.
	if ranMain {
		if n, ok := value.AsNumber(result); ok {
			return int(n)
		}
	}
	return 0
}

// scriptArgs is what a run program sees through the args builtin. It mirrors Go's
// os.Args: element 0 is the program name and the rest are the words after the
// script path, so `twill run cli.tw check foo` hands the program
// ["twill", "check", "foo"] and a self-hosted main that drops the first element
// (as os.Args[1:] does) is left with ["check", "foo"].
func scriptArgs(path string) []string {
	rest := os.Args[1:]
	for i, a := range rest {
		if a == path {
			return append([]string{"twill"}, rest[i+1:]...)
		}
	}
	return []string{"twill"}
}

func checkOnly(path string) int {
	if err := interp.CheckLegacyExt(path); err != nil {
		fmt.Fprintf(os.Stderr, "twill: %s\n", err)
		return 1
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "twill: cannot read file %q\n", path)
		return 1
	}
	prog, perr := parser.Parse(string(src))
	if perr != nil {
		reportError(path, string(src), perr)
		return 1
	}
	diags := checker.CheckFile(prog, path)
	if len(diags) == 0 {
		fmt.Printf("%s: no shape problems found\n", path)
		return 0
	}
	printDiags(path, string(src), diags)
	// A file whose only findings are warnings has nothing wrong with it, so the
	// check passes. They are still printed: that is the whole point of them.
	if checker.CountErrors(diags) > 0 {
		return 1
	}
	return 0
}

// printDiags writes one diagnostic per line, prefixed with the file and line so
// an editor can jump to them, each followed by the offending source line. A
// warning says so rather than calling itself a shape error, which it is not.
// src/main.tw's print_diags is the same function and the two must agree
// character for character.
func printDiags(path, src string, diags []checker.Diagnostic) {
	for _, d := range diags {
		label := "shape error"
		if !d.IsError() {
			label = "warning"
		}
		fmt.Fprintf(os.Stderr, "%s:%d: %s: %s\n", path, d.Line, label, d.Msg)
		showContext(src, d.Line, 0)
	}
}

// sourceArgs resolves the paths for a command that works on source files: it
// rejects a flag nobody knows, expands directories, and exits 2 with the given
// usage line when nothing is left to work on. usage is the one-line form shown
// for an invocation with no paths at all.
func sourceArgs(args []string, usage string, known map[string]bool) []string {
	if bad := unknownFlag(args[1:], known); bad != "" {
		fmt.Fprintf(os.Stderr, "twill: unknown flag %q\n", bad)
		os.Exit(2)
	}
	given := nonFlagArgs(args[1:])
	if len(given) == 0 {
		fmt.Fprintf(os.Stderr, "usage: twill %s\n", usage)
		os.Exit(2)
	}
	files, err := expandSourcePaths(given)
	if err != nil {
		fmt.Fprintf(os.Stderr, "twill: %s\n", err)
		os.Exit(2)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "twill: no .tw files found")
		os.Exit(2)
	}
	return files
}

// formatOne returns a file's formatted text alongside the text it started with,
// so a caller can tell whether formatting would change it without formatting it
// twice. An error has already been reported when it comes back.
func formatOne(path string) (out, src string, err error) {
	if lerr := interp.CheckLegacyExt(path); lerr != nil {
		fmt.Fprintf(os.Stderr, "twill: %s\n", lerr)
		return "", "", lerr
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "twill: cannot read file %q\n", path)
		return "", "", rerr
	}
	formatted, ferr := format.Source(string(b))
	if ferr != nil {
		reportError(path, string(b), ferr)
		return "", "", ferr
	}
	return formatted, string(b), nil
}

func repl() {
	fmt.Printf("Twill %s, a tensor-first differentiable language.\n", version)
	fmt.Println("Type an expression, or :help / :quit. Multi-line blocks continue")
	fmt.Println("until brackets balance.")
	ip := interp.New(nil)
	sc := bufio.NewScanner(os.Stdin)
	var buf strings.Builder
	fmt.Print("twill> ")
	for sc.Scan() {
		line := sc.Text()
		if buf.Len() == 0 {
			trimmed := strings.TrimSpace(line)
			switch trimmed {
			case ":quit", ":q":
				return
			case ":help":
				usage()
				fmt.Print("twill> ")
				continue
			case "":
				fmt.Print("twill> ")
				continue
			}
			// `:type e` and `:shape e` answer what the checker inferred, without
			// running anything. A tensor-first language's most useful REPL
			// question is what shape something is, and asking it by evaluating
			// and printing the value does not answer it for anything large.
			if verb, expr, ok := replQuery(trimmed); ok {
				fmt.Println(describeExpr(verb, expr))
				fmt.Print("twill> ")
				continue
			}
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		if needsMoreInput(buf.String()) {
			fmt.Print("   ...> ")
			continue
		}
		src := buf.String()
		buf.Reset()
		v, err := ip.Run(src)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
		} else if _, isUnit := v.(value.Unit); !isUnit && v != nil {
			fmt.Println(value.Format(v))
		}
		fmt.Print("twill> ")
	}
	fmt.Println()
}

// needsMoreInput reports whether a REPL buffer has unbalanced brackets (or an
// unterminated string), meaning the user should keep typing.
func needsMoreInput(src string) bool {
	toks, err := lexer.Tokenize(src)
	if err != nil {
		return true // e.g. an unterminated string literal
	}
	depth := 0
	for _, t := range toks {
		if t.Kind != lexer.PUNCT {
			continue
		}
		switch t.Value {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		}
	}
	return depth > 0
}

// reportError prints an error against the file it came from: a source-line
// excerpt and a caret where the error carries a position, and the file's name
// in front of the message where it does not. src/main.tw's report_syntax and
// report_format_error are the same function and the two must agree character
// for character.
func reportError(path, src string, err error) {
	switch e := err.(type) {
	case *lexer.SyntaxError:
		fmt.Fprintf(os.Stderr, "%s:%d:%d: syntax error: %s\n", path, e.Line, e.Col, e.Msg)
		showContext(src, e.Line, e.Col)
	case *interp.RuntimeError:
		fmt.Fprintf(os.Stderr, "%s:%d: runtime error: %s\n", path, e.Line, e.Msg)
		showContext(src, e.Line, 0)
	default:
		// An error with no position in it still belongs to a file, and it says
		// so. While one invocation meant one file the caller knew which file a
		// bare `error: ...` was about; over a directory they do not, and
		// `twill fmt --check .` in this repository writes ten of these.
		fmt.Fprintf(os.Stderr, "%s: error: %s\n", path, err.Error())
	}
}

// showContext prints the source line and, when col > 0, a caret beneath it.
func showContext(src string, line, col int) {
	if src == "" || line < 1 {
		return
	}
	lines := strings.Split(src, "\n")
	if line > len(lines) {
		return
	}
	prefix := fmt.Sprintf("  %d | ", line)
	fmt.Fprintf(os.Stderr, "%s%s\n", prefix, lines[line-1])
	if col > 0 {
		fmt.Fprintf(os.Stderr, "%s^\n", strings.Repeat(" ", len(prefix)+col-1))
	}
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// nonFlagArgs drops the leading-dash words, leaving the positional paths. Used
// by `twill test`, whose arguments are any number of paths in any order with an
// optional -v among them.
// flagValue reads `--flag value` or `--flag=value`, returning "" when the flag
// is absent.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// valueFlags are the flags that take a separate following argument, so that
// argument is not mistaken for a path.
var valueFlags = map[string]bool{"--filter": true}

func nonFlagArgs(args []string) []string {
	var out []string
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			skip = valueFlags[a]
			continue
		}
		out = append(out, a)
	}
	return out
}

// replQuery splits a `:type expr` or `:shape expr` line into its verb and its
// expression. It reports false for anything else, including a bare verb with
// nothing after it.
func replQuery(line string) (verb, expr string, ok bool) {
	for _, v := range []string{":type", ":shape"} {
		if strings.HasPrefix(line, v+" ") {
			rest := strings.TrimSpace(line[len(v):])
			if rest != "" {
				return v, rest, true
			}
		}
	}
	return "", "", false
}

// describeExpr answers a REPL query by checking the expression, not running it:
// the answer to "what shape is this" should not depend on being able to
// afford the value.
func describeExpr(verb, expr string) string {
	desc, err := checker.Describe(expr)
	if err != nil {
		return err.Error()
	}
	if verb == ":shape" {
		return desc.Shape
	}
	return desc.Type
}

func usage() {
	fmt.Printf(`Twill %s

Usage:
  twill <file.tw>            Shape-check, then run a program
  twill run <file.tw>        Same as above
  twill run <file> --no-check   Run without the static shape check
  twill run <file> --dump=canonical   Run, then dump top-level bindings exactly
  twill check <path ...>     Shape-check only, no execution
  twill fmt <path ...>       Print canonically formatted source
  twill fmt <paths> --write  Format the files in place
  twill fmt <paths> --check  Name the files that would change; exit 1 if any
  twill test [path ...]      Run every *_test.tw under the paths (default: .)
  twill test <paths> -v      Show each suite's output, not only failures'
  twill test --filter <sub>  Run only the suites whose path contains <sub>
  twill doctor               Report what is installed, and what looks wrong
  twill lsp                  Language server on stdin/stdout, for an editor
  twill                      Start the REPL
  twill --version            Print the version
  twill --version --verbose  ...with the build and library it came from

In the REPL: :help, :quit, :type <expr>, :shape <expr>
`, version)
}
