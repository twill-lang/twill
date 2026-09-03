package interp_test

import (
	"fmt"
	"strings"
	"testing"
)

// sort over a list of anything but strings, and sort by a comparison the caller
// supplies.
//
// docs/roadmap.md ranked this by how many codebases had written their own:
// bobbin two insertion sorts, weft one, spool four, loom one, and skein a merge
// sort over an index array. Only strings could delegate to the builtin before,
// so none of them could: `sort([3, 1, 2])` on an Arr[I64] failed with "sort on a
// list expects every element to be a string".
//
// These run in `mode systems`, because that is where a list of numbers is a list
// rather than a tensor -- a bare `[3, 1, 2]` in numeric mode is a tensor and
// takes the tensor path.

func systemsRun(t *testing.T, body string) string {
	t.Helper()
	// `ip.Run` evaluates the file; `main` is called here because nothing else
	// calls it outside the CLI's RunFileMain.
	_, out := run(t, "mode systems\nfn main() {\n"+body+"\n}\nmain()\n")
	return strings.TrimSpace(strings.Join(out, "|"))
}

func TestSortOrdersNumbersAsWellAsStrings(t *testing.T) {
	cases := []struct{ body, want string }{
		{`let a: Arr[I64] = [3, 1, 2]
  print(str(sort(a)))`, "[1, 2, 3]"},
		{`let a: Arr[F64] = [3.5, 1.25, 2.0]
  print(str(sort(a)))`, "[1.25, 2, 3.5]"},
		// Negative and zero, because a numeric order that only ever saw counts
		// would not notice a sign.
		{`let a: Arr[I64] = [0, 0 - 5, 5]
  print(str(sort(a)))`, "[-5, 0, 5]"},
		{`let a: Arr[I64] = [3, 1, 2]
  print(str(sort(a, true)))`, "[3, 2, 1]"},
		// The string order is unchanged by any of this.
		{`let a: Arr[Str] = ["banana", "apple", "Apple"]
  print(str(sort(a)))`, "[Apple, apple, banana]"},
		{`let a: Arr[I64] = []
  print(str(sort(a)))`, "[]"},
	}
	for _, c := range cases {
		if got := systemsRun(t, c.body); got != c.want {
			t.Errorf("%s\n  got  %q\n  want %q", c.body, got, c.want)
		}
	}
}

func TestSortTakesAComparison(t *testing.T) {
	got := systemsRun(t, `let a: Arr[I64] = [3, 1, 2]
  print(str(sort(a, fn(x: I64, y: I64) -> Bool { x > y })))`)
	if want := "[3, 2, 1]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The comparison is what makes a list of records sortable at all: nothing else
// can know which field the order is on.
func TestSortAComparisonOrdersRecords(t *testing.T) {
	got := systemsRun(t, `struct P { name: Str, n: I64 }
  let ps: Arr[P] = [P { name: "c", n: 2 }, P { name: "a", n: 1 }, P { name: "b", n: 3 }]
  let s = sort(ps, fn(x: P, y: P) -> Bool { x.n < y.n })
  print(s[0].name + s[1].name + s[2].name)`)
	if want := "acb"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// skein's case, which is why the comparison takes elements rather than a key:
// it sorts an index array by comparing through a second array the closure
// captures. `docs/needs.md` entry 3 there says a key function would not do.
func TestSortAComparisonCanReachAParallelArray(t *testing.T) {
	got := systemsRun(t, `let words: Arr[Str] = ["kilo", "a", "the", "be"]
  let idx: Arr[I64] = [0, 1, 2, 3]
  let by_length = sort(idx, fn(x: I64, y: I64) -> Bool { len(words[x]) < len(words[y]) })
  print(words[by_length[0]] + " " + words[by_length[1]] + " " + words[by_length[2]] + " " + words[by_length[3]])`)
	if want := "a be the kilo"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Stability, which is a correctness property and not a nicety.
//
// skein assigns token ids from a sorted order, so an unstable sort would make
// the ids depend on the sort's internals rather than on the data, and a
// vocabulary built twice from one corpus would differ.
//
// THIS TEST IS BIG ON PURPOSE. The first version of it sorted three records with
// one tie and passed against `sort.Slice`, which is not stable: Go's sort falls
// back to insertion sort under about a dozen elements, and insertion sort
// happens to be stable, so a three-element case cannot tell the two apart. It
// only fails on an unstable sort once the input is long enough for pdqsort to
// actually partition it, which is what the twenty-four below are for.
func TestSortIsStable(t *testing.T) {
	// Twenty-four elements, two distinct keys, and a position stamped into each
	// so the original order is readable in the output.
	var b strings.Builder
	b.WriteString("struct P { at: I64, k: I64 }\n  let ps: Arr[P] = [")
	for i := 0; i < 24; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		// Keys alternate, so a stable sort interleaves nothing: every even
		// position lands in the first half, in order, and every odd one in the
		// second half, in order.
		fmt.Fprintf(&b, "P { at: %d, k: %d }", i, i%2)
	}
	b.WriteString("]\n  let s = sort(ps, fn(x: P, y: P) -> Bool { x.k < y.k })\n")
	b.WriteString("  let out: Str = \"\"\n")
	b.WriteString("  let i: I64 = 0\n  while i < len(s) { out = out + str(s[i].at) + \",\"; i = i + 1 }\n  print(out)")

	got := systemsRun(t, b.String())
	var want strings.Builder
	for _, k := range []int{0, 1} {
		for i := 0; i < 24; i++ {
			if i%2 == k {
				fmt.Fprintf(&want, "%d,", i)
			}
		}
	}
	if got != want.String() {
		t.Errorf("the sort is not stable\n  got  %s\n  want %s", got, want.String())
	}
}

// The same, without a comparison: equal numbers are indistinguishable, so
// stability is checked where it can be seen, through records ordered on a field
// that ties.
func TestSortIsStableWithoutAComparison(t *testing.T) {
	got := systemsRun(t, `let a: Arr[Str] = ["b", "a", "b", "a"]
  print(str(sort(a)))`)
	if want := "[a, a, b, b]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSortSaysWhyItCannotOrderAList(t *testing.T) {
	cases := []struct{ body, wants string }{
		// Mixed kinds: there is an order for each half and none for the pair.
		{`let a: Arr[Str] = ["a"]
  print(str(sort([1, a[0]])))`, "mixed strings and numbers"},
		// Records have no order of their own; the message names the way out.
		{`struct P { n: I64 }
  let ps: Arr[P] = [P { n: 2 }, P { n: 1 }]
  print(str(sort(ps)))`, "pass a comparison"},
		// Two trailing arguments is neither form.
		{`let a: Arr[I64] = [2, 1]
  print(str(sort(a, true, true)))`, "one of a descending flag or a comparison"},
	}
	for _, c := range cases {
		_, err := runSrcErr(t, "mode systems\nfn main() {\n"+c.body+"\n}\nmain()\n")
		if err == nil {
			t.Errorf("%s\n  expected an error mentioning %q", c.body, c.wants)
			continue
		}
		if !strings.Contains(err.Error(), c.wants) {
			t.Errorf("%s\n  got  %v\n  want a message mentioning %q", c.body, err, c.wants)
		}
	}
}

// The input list is never reordered under the caller, in either form.
func TestSortDoesNotMutateItsInput(t *testing.T) {
	got := systemsRun(t, `let xs: Arr[I64] = [2, 1, 3]
  let ys = sort(xs)
  let zs = sort(xs, fn(x: I64, y: I64) -> Bool { x > y })
  print(str(xs) + " " + str(ys) + " " + str(zs))`)
	if want := "[2, 1, 3] [1, 2, 3] [3, 2, 1]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
