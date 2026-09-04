// tracestats runs each .tw file given and reports what the tracer did with it,
// so "how much of the language compiles" is a measurement rather than a guess.
//
// nodes and computed are the two columns that count the same thing: tensor
// operations recorded, and tensor operations whose arithmetic was performed. The
// rest count events. A program whose computed column is well under its nodes
// column is one the liveness rule is deleting work from, which is what
// docs/CODEGEN.md section 12.1 is about.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/twill-lang/twill/internal/interp"
)

func main() {
	files := os.Args[1:]
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tracestats <file.tw>...")
		os.Exit(2)
	}

	fmt.Printf("%-30s %8s %8s %8s %8s %8s %8s %8s %7s\n",
		"program", "nodes", "computed", "scopes", "traced", "compiled", "replayed", "escapes", "miss")

	var traced, total int
	for _, f := range files {
		ip := interp.New(func(string) {})
		ip.SetTracing(true)
		if _, _, err := ip.RunFileMain(f, nil); err != nil {
			fmt.Printf("%-34s failed: %v\n", filepath.Base(f), err)
			continue
		}
		s := ip.TraceStats()
		total++
		if s.Nodes > 0 {
			traced++
		}
		fmt.Printf("%-30s %8d %8d %8d %8d %8d %8d %8d %7d\n",
			filepath.Base(f), s.Nodes, s.Computed, s.Scopes, s.Traced, s.Compiled,
			s.Replayed, s.Escapes, s.CacheMiss)
	}
	fmt.Printf("\n%d of %d programs produced traced nodes\n", traced, total)
}
