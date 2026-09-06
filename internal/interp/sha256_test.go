package interp_test

import "testing"

// SHA-256 as a builtin. std/hash.tw is the specification: it is the same digest
// written in twill over I64, and spool's tests/sha256_test.tw is the vector
// suite that holds it to the published answers.
//
// The digest is a format constant. spool's lockfiles, selvedge's archives and
// warp's cache keys each write one down and read it back later, so the builtin
// and std/hash.tw have to answer the same thing for every input or an artefact
// hashed by one toolchain stops verifying under the other. The vectors below
// are the ones spool's suite already asserts, copied so this file fails on its
// own if the two ever part.

func TestSha256OfAStringIsLowercaseHex(t *testing.T) {
	out := runI64(t, `
print(sha256(""))
print(sha256("abc"))
print(sha256("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"))
`)
	expectLines(t, out,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1")
}

// 55, 56 and 64 bytes straddle the point where the padding spills into a second
// block, which is where a hand-written implementation goes wrong.
func TestSha256AtThePaddingBoundaries(t *testing.T) {
	out := runI64(t, `
fn repeat(s: Str, n: I64) -> Str {
  let out = bytes_new()
  let i = 0
  while i < n {
    bytes_push(out, s[0])
    i = i + 1
  }
  bytes_to_str(out)
}
print(sha256(repeat("a", 55)))
print(sha256(repeat("a", 56)))
print(sha256(repeat("a", 64)))
`)
	expectLines(t, out,
		"9f4390f8d30c2dd92ec9f095b65e2b9ae9b0a925a5258e241c9f1e910f734318",
		"b35439a4ac6f0948b6d6f9e3c6af0f5f590ce20f1bde7090ef7970686ec6738a",
		"ffe054fe7ae0cb6dc65c3af9b61d5209f439851db43d0ba5997337df154668eb")
}

// sha256_bytes hashes a buffer, and a buffer holding the same bytes as a string
// hashes to the same digest. That is the property spool needs: it reads a file
// as text and selvedge writes one as bytes.
func TestSha256BytesAgreesWithSha256OverTheSameBytes(t *testing.T) {
	out := runI64(t, `
let b = bytes_new()
bytes_push(b, 97)
bytes_push(b, 98)
bytes_push(b, 99)
print(sha256_bytes(b))
print(sha256_bytes(b) == sha256("abc"))
let e = bytes_new()
print(sha256_bytes(e) == sha256(""))
`)
	expectLines(t, out,
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"true", "true")
}

// A digest is 64 characters of lower-case hex, always, including for input with
// a high byte in it.
func TestSha256DigestShape(t *testing.T) {
	out := runI64(t, `
let d = sha256("anything")
print(len(d))
let b = bytes_new()
bytes_push(b, 255)
bytes_push(b, 0)
bytes_push(b, 128)
print(len(sha256_bytes(b)))
print(sha256_bytes(b))
`)
	expectLines(t, out, "64", "64",
		"ef192b7af54e943f206ab27075ec1805384c972c9959fc5820f1fa7d5268fcef")
}

// The builtin and std/hash.tw are one digest with two implementations, and this
// is the test that says so. It compares them over every length from 0 to 70
// bytes, which walks the message across both padding boundaries (55/56 in the
// first block, 119/120 in the second is out of range here, so 63/64/65 is the
// one that matters), and over a byte range that includes 0 and 255 so a
// sign-extension mistake on either side shows up.
//
// It is slow by the standards of this package because std/hash.tw runs the
// compression function in the interpreter, so the range stops at 70 rather than
// at a block count.
func TestSha256BuiltinAgreesWithStdHash(t *testing.T) {
	dir := t.TempDir()
	out := runFile(t, dir, `mode systems
import "std/hash" as sha

fn body(n: I64) -> Str {
  let b = bytes_new()
  let i = 0
  while i < n {
    bytes_push(b, (i * 37 + 11) band 255)
    i = i + 1
  }
  bytes_to_str(b)
}

let bad = 0
let n = 0
while n <= 70 {
  let s = body(n)
  if sha.hash_str(s) != sha256(s) {
    print(n)
    print(sha.hash_str(s))
    print(sha256(s))
    bad = bad + 1
  }
  n = n + 1
}
print(bad)
`)
	expectLines(t, out, "0")
}
