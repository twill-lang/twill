package interp_test

import (
	"os"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
	"github.com/twill-lang/twill/internal/value"
)

// read_line reads standard input one line at a time as an Opt, so a Twill
// program can drive an interactive loop. This feeds two lines through a pipe and
// checks that two reads see them and the third sees end of input.
func TestReadLineReadsStdinLines(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	go func() {
		w.WriteString("first\nsecond\n")
		w.Close()
	}()

	src := `
let out = list()
let going = true
while going {
  match read_line() {
    Some(l) => { out = append(out, l) },
    None => { going = false }
  }
}
out
`
	ip := interp.New(func(string) {})
	v, err := ip.Run(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	out := value.Format(v)
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("expected both lines, got %q", out)
	}
}
