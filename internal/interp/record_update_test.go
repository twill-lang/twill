package interp_test

import (
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
)

// `{ ..base, field: value }` evaluates to a copy of base with the named fields
// replaced. Everything below is about what "copy" means, because that is the
// only part of the feature a reader can get wrong.

func TestRecordUpdateReplacesOnlyWhatItNames(t *testing.T) {
	if got := scalar(t, "let base = { a: 3.0, b: 4.0 }\nlet m = { ..base, b: 10.0 }\nm.a + m.b"); got != 13 {
		t.Errorf("got %v, want 13", got)
	}
	// With no fields at all it is a plain copy.
	if got := scalar(t, "let base = { a: 3.0, b: 4.0 }\nlet m = { ..base }\nm.a + m.b"); got != 7 {
		t.Errorf("copy got %v, want 7", got)
	}
}

func TestRecordUpdateLeavesTheBaseAlone(t *testing.T) {
	if got := scalar(t, "let base = { a: 3.0 }\nlet m = { ..base, a: 99.0 }\nbase.a"); got != 3 {
		t.Errorf("the update wrote through to its base: got %v, want 3", got)
	}
}

// Field order is the base's, with anything the update adds after it, so a
// printed record reads in the order the base was written rather than in the
// order the update happened to name things.
func TestRecordUpdateKeepsFieldOrder(t *testing.T) {
	_, out := run(t, "let base = { z: 1.0, a: 2.0 }\nprint({ ..base, a: 3.0, m: 4.0 })")
	if len(out) != 1 || strings.TrimSpace(out[0]) != "{z: 1, a: 3, m: 4}" {
		t.Errorf("got %q, want {z: 1, a: 3, m: 4}", out)
	}
}

// The copy is shallow, and deliberately: a field holding a container hands over
// the same container, which is exactly what writing `{ tags: base.tags }` out by
// hand already does. Anything else would make one spelling of a record literal
// mean something different from the other.
func TestRecordUpdateCopiesShallowly(t *testing.T) {
	src := `mode systems
let base = { tags: arr_new(), n: 1 }
let m = { ..base, n: 2 }
push(m.tags, "shared")
print(len(base.tags))
print(m.n)
`
	_, out := run(t, src)
	got := strings.Join(out, "")
	if got != "12" {
		t.Errorf("the base's list did not see the push through the copy: got %q, want \"12\"", got)
	}
	// The same program written without `..`, for the comparison the rule is
	// stated against.
	byHand := `mode systems
let base = { tags: arr_new(), n: 1 }
let m = { tags: base.tags, n: 2 }
push(m.tags, "shared")
print(len(base.tags))
print(m.n)
`
	_, out2 := run(t, byHand)
	if strings.Join(out2, "") != got {
		t.Errorf("`..base` and the hand-written copy disagree:\n  update:  %q\n  by hand: %q", got, strings.Join(out2, ""))
	}
}

// The copy carries the base's struct name, which is not cosmetic: a field
// assignment reads it to find the field's declared type, and `{}` at a
// Dict-typed field is an empty dictionary only because of that lookup. Without
// the name the same line stores an empty record and the dict_set below fails.
func TestTypedRecordUpdateKeepsTheStructName(t *testing.T) {
	src := `mode systems
struct C { versions: Dict[Str, I64], n: I64 }
let base: C = C { versions: {}, n: 1 }
let m = { ..base, n: 2 }
m.versions = {}
dict_set(m.versions, "k", 7)
print(dict_must(m.versions, "k"))
`
	_, out := run(t, src)
	if len(out) != 1 || strings.TrimSpace(out[0]) != "7" {
		t.Errorf("got %q, want 7", out)
	}
}

func TestRecordUpdateOfANonRecordFails(t *testing.T) {
	ip := interp.New(func(string) {})
	_, err := ip.Run("mode systems\nlet n: I64 = 3\nlet bad = { ..n, y: 1 }\n")
	if err == nil {
		t.Fatal("updating a number was supposed to fail")
	}
	const want = "the base of a record update must be a record"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("got %q, want it to contain %q", err.Error(), want)
	}
}

// The value an update builds is one the language could already build:
// `with_field`, the builtin std/frame adds a column with, which copies a record
// shallowly and sets one field. Pinning the two against each other is what keeps
// a later change to one of them from splitting the meaning of the other.
func TestRecordUpdateAgreesWithWithField(t *testing.T) {
	const base = "let base = { a: 1.0, b: 2.0 }\n"
	wantBool(t, base+`{ ..base, b: 3.0 } == with_field(base, "b", 3.0)`, true)
	wantBool(t, base+`{ ..base, c: 4.0 } == with_field(base, "c", 4.0)`, true)
	wantBool(t, base+`{ ..base } == base`, true)
}

// grad walks a record's structure, so a model built by updating another one is
// still a parameter tree.
func TestGradThroughARecordUpdate(t *testing.T) {
	src := "fn loss(m) = sum(m.w) + m.b\nlet base = { w: [1.0, 2.0], b: 0.5 }\ngrad(loss)({ ..base, b: 1.5 }).w[1]"
	if got := scalar(t, src); got != 1 {
		t.Errorf("d/dw got %v, want 1", got)
	}
}
