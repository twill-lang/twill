# Conformance cases

One twill program each, run by `make conformance-check` under both
implementations as two processes, with the exit code, stdout and stderr
compared byte for byte. There is no allow-list: a case is checked in only once
both sides already agree, so a divergence here is a regression.

Two rules make a case comparable rather than merely written down:

* **Print a fact, not a reading.** A clock, a temporary directory's name and a
  process's memory are different on every run, so a case prints what is true of
  them (`elapsed >= 0`, `the directory exists`) and never the value itself.
  Anything printed has to be the same on two runs of the same binary before it
  can say anything about two different ones.
* **Leave nothing behind.** A case that writes files makes its own directory
  with `temp_dir` and removes it, because the two runs share one staging
  directory and the second must not see the first one's debris.
