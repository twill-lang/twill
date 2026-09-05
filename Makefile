BINARY := twill
PKG := ./cmd/twill

.PHONY: build test vet fmt race lint check ci bench examples install clean conformance conformance-check diff

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# The race pass, with the same flags and budget CI uses. -short skips the two
# model-training examples and the self-hosted differential runs, which is what
# keeps this inside the timeout; `test` above runs all of them.
race:
	go test -race -short -timeout 25m ./internal/tensor/ ./internal/interp/

# The two linters CI runs. They fetch a tool, so they need the network, which is
# why they are not in `check`.
lint:
	out="$$(go run golang.org/x/tools/cmd/deadcode@latest -test ./...)"; if [ -n "$$out" ]; then echo "$$out"; echo "dead code found"; exit 1; fi
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

# What CI runs, minus the two linters that need the network. Run this before
# tagging a release.
#
# The race pass is in here because leaving it out is how 1.6.5 shipped with a CI
# failure: the local gate was `vet test` and gofmt, CI was that plus a race pass
# and two linters, and the suite had grown past the race budget without anything
# local saying so. A gate that does not match CI is a gate that lets things
# through.
check: build vet test race
	gofmt -l . | tee /dev/stderr | (! read)

# --- conformance ------------------------------------------------------------
#
# Twill has two implementations and the README says they agree. That is true of
# the lexer, the parser, the checker and the formatter, and it is not true of
# the evaluator: roughly half the names in the shared builtin table have no
# implementation under src/eval.tw. Nothing in the build said so, because
# nothing compared them, so these three targets are the comparison. The count is
# in docs/conformance.md, which is generated, and is deliberately not repeated
# here: a number nobody regenerates is a number that goes wrong quietly.
#
# They are not in `check` because the self-hosted side runs the Go interpreter
# interpreting src/*.tw, which is the slowest thing in the repository. They are
# in `ci`, and they have their own CI job, so a new divergence fails the build
# on the pull request that introduced it.

# Regenerate docs/conformance.md. Run this after adding a builtin to either
# implementation, and commit the result.
conformance: build
	go run ./tools/conformance builtins -bin ./$(BINARY)

# The gate. Three separate claims:
#   1. docs/conformance.md matches what the two implementations actually do.
#   2. Every std/tests suite produces the same bytes under both, except the ones
#      on testdata/conformance/suite-allow.txt, each of which is keyed to the
#      divergence it excuses rather than to the suite's name.
#   3. The fixture corpus still matches its recorded goldens, except the ones on
#      testdata/conformance/golden-allow.txt, each keyed to the kind of finding
#      it excuses.
# Both allow-lists may only shrink. A new disagreement is a failure, and so is a
# divergence that has changed into a different one under an existing line.
conformance-check: build
	go run ./tools/conformance builtins -bin ./$(BINARY) -check
	go run ./tools/conformance suites -bin ./$(BINARY)

# tools/diff/run against the checked-in goldens. This target is the reason the
# harness exists: it was written, committed, and then referenced by neither the
# Makefile nor the CI workflow, so it had never been run and had been failing
# for months. The allow-list holds the findings that were already there, with
# what was measured and when at the top of the file.
diff: build
	go run ./tools/diff/run -corpus testdata -bin ./$(BINARY) -verify \
		-allow testdata/conformance/golden-allow.txt

# Everything, including the linters and the conformance gate. What the release
# gate should be.
ci: check lint conformance-check diff

bench:
	go test -run=XXX -bench=. ./internal/tensor/

examples: build
	./$(BINARY) examples/hello.tw
	./$(BINARY) examples/autodiff.tw
	./$(BINARY) examples/linreg.tw
	./$(BINARY) examples/nn_xor.tw
	./$(BINARY) examples/classifier.tw
	./$(BINARY) check examples/shapes.tw

install:
	go install $(PKG)

clean:
	rm -f $(BINARY) $(BINARY).exe
