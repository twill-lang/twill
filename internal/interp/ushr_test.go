package interp_test

import "testing"

// ushr is the logical right shift: the operand's bit pattern is read as
// unsigned, so a set sign bit moves down rather than smearing ones from the
// top. It is a call and not an operator, which is why nothing here is written
// infix.
//
// std/float.tw and selvedge both hand-rolled it out of shr, band, bnot and shl
// with a sign test in front. These cases are the ones that hand-rolling gets
// wrong: the whole negative range, and a count of zero, where clearing the sign
// bit and adding it back at bit 63 - 0 puts it where it already was.

func TestUshrIsLogicalNotArithmetic(t *testing.T) {
	out := runI64(t, `
print(ushr(0 - 1, 1))
print(shr(0 - 1, 1))
print(ushr(0 - 1, 63))
print(ushr(0 - 9223372036854775808, 63))
print(ushr(8, 1))
print(ushr(0 - 2, 1))
`)
	expectLines(t, out,
		"9223372036854775807", // (2^64 - 1) >> 1
		"-1",                  // shr keeps the sign
		"1",
		"1",
		"4",
		"9223372036854775807",
	)
}

// A count of zero is the identity, for a negative operand too.
func TestUshrByZeroIsIdentity(t *testing.T) {
	out := runI64(t, `
print(ushr(0 - 1, 0))
print(ushr(0 - 9223372036854775808, 0))
print(ushr(0, 0))
print(ushr(1234567890123456789, 0))
`)
	expectLines(t, out, "-1", "-9223372036854775808", "0", "1234567890123456789")
}

// The count is masked to 0..63, the way shl and shr mask theirs
// (docs/language-guide.md), so a shift is always defined rather than depending
// on the host.
func TestUshrMasksItsCount(t *testing.T) {
	out := runI64(t, `
print(ushr(0 - 1, 64) == ushr(0 - 1, 0))
print(ushr(0 - 1, 65) == ushr(0 - 1, 1))
print(ushr(1024, 74) == ushr(1024, 10))
`)
	expectLines(t, out, "true", "true", "true")
}
