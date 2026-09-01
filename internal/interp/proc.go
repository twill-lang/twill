package interp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/twill-lang/twill/internal/value"
)

// run: start a program, wait for it, and answer with what it printed.
//
//	run(program: Str, argv: Arr[Str], dir: Str) -> Res[Str, Str]
//
// spool `docs/needs.md` entry 1, which was the last open entry on that list and
// the only thing between spool and fetching a package. It asked for exactly
// this signature. warp asks the same question in the other direction and would
// take an HTTPS client instead; a process interface is the smaller surface and
// the one a self-hosted toolchain needs anyway to drive a linker.
//
// WHY THERE IS NO SHELL HERE, EVER
//
// The program and its arguments are separate values and stay separate all the
// way to `execve`. Nothing here parses a command line, splits on whitespace,
// expands a glob, or reads `$SHELL`. A package manager's arguments are branch
// names, tags and URLs from a manifest a stranger wrote, and a shell is the one
// component that would turn `v1.0; rm -rf ~` from an argument into a command.
// The cost is that a caller who wants a pipeline has to write it as two runs,
// which is the right trade for a language whose file I/O is already this
// conservative. A convenience wrapper taking one string must not be added later
// for the same reason -- see the test that pins this.
//
// WHAT IT REFUSES
//
// Running a program is a bigger widening of what a `.tw` file can do than
// anything else in the runtime, and spool's entry says so itself: it should be
// a considered decision rather than a side effect of wanting a package manager.
// So there is an off switch that costs a program nothing to respect --
// TWILL_NO_EXEC, set to anything non-empty, makes every `run` answer Err
// without starting anything. Err rather than an abort, so a caller can degrade
// (spool can vendor from a path source with git unavailable) instead of dying,
// and the message names the variable so nobody debugs it as a missing binary.
//
// The environment is otherwise inherited whole. That is deliberate and it is
// the entire reason spool shells out to git rather than speaking the protocol:
// it borrows the user's existing credentials, proxy configuration and host
// keys. An allowlist here would silently break authentication against a private
// repository, which is the case the feature exists for.
//
// WHAT COUNTS AS A FAILURE
//
// Ok carries stdout, and only when the program exited 0. Anything else is Err:
// a program that could not start, one killed by a signal, one that exited
// non-zero. stderr is never merged into Ok -- a caller parsing `git rev-list`
// output must not have a warning line spliced into the middle of it -- and on
// the Err path it is the message, because that is where a caller looks for why.
const noExecVar = "TWILL_NO_EXEC"

// procErr is the Err side, as a Str, the way every other fallible builtin
// answers.
func procErr(msg string) value.Value {
	return &value.Variant{Name: "Err", Payload: value.Str(msg), HasPayload: true}
}

// describeExit says what happened to a program in one line: the exit status, or
// the signal, followed by whatever it wrote to stderr. The stderr is trimmed of
// trailing newlines only -- its interior is the diagnostic and is left alone.
func describeExit(program string, err error, stderr string) string {
	msg := fmt.Sprintf("%s: %s", program, err.Error())
	if trimmed := strings.TrimRight(stderr, "\r\n"); trimmed != "" {
		msg += ": " + trimmed
	}
	return msg
}

// registerProc defines the process builtins. def and asStr are the registrar
// and the string reader from builtins.go, the same pair registerFS takes.
func (ip *Interp) registerProc(def func(string, int, bool, func([]value.Value) (value.Value, error)), asStr func(value.Value) (string, bool)) {
	def("run", 3, false, func(a []value.Value) (value.Value, error) {
		program, ok := asStr(a[0])
		if !ok {
			return nil, fmt.Errorf("run expects a program name")
		}
		list, ok := a[1].(*value.List)
		if !ok {
			return nil, fmt.Errorf("run expects an Arr[Str] of arguments")
		}
		argv := make([]string, len(list.Items))
		for i, item := range list.Items {
			s, ok := asStr(item)
			if !ok {
				return nil, fmt.Errorf("run expects every argument to be a string")
			}
			argv[i] = s
		}
		dir, ok := asStr(a[2])
		if !ok {
			return nil, fmt.Errorf("run expects a working directory")
		}

		// An empty program name would reach exec.Command and come back as a
		// confusing lookup failure; say what is actually wrong instead.
		if program == "" {
			return procErr("run: no program named"), nil
		}
		if os.Getenv(noExecVar) != "" {
			return procErr(fmt.Sprintf("run: refused to start %q because %s is set", program, noExecVar)), nil
		}

		cmd := exec.Command(program, argv...)
		// An empty dir means "where the running program's relative paths point",
		// which is the base read_file and write_file already resolve against, so
		// a caller does not have to know two rules. Any other dir is resolved
		// the same way, so a relative one means the same thing here as there.
		if dir == "" {
			cmd.Dir = ip.currentDir()
		} else {
			cmd.Dir = ip.resolvePath(dir)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		// No stdin. A program that waits for input would hang a build with no
		// way to tell it had, and every caller so far -- git, a linker -- is
		// non-interactive. Passing os.Stdin would also hand a package's build
		// script the user's terminal.
		cmd.Stdin = nil

		if err := cmd.Run(); err != nil {
			return procErr(describeExit(program, err, stderr.String())), nil
		}
		return &value.Variant{Name: "Ok", Payload: value.Str(stdout.String()), HasPayload: true}, nil
	})
}
