# AGENTS.md

You're working on a project called "clai".

## Always read:

- ./internal/cli/cli.go - This contains usage which gives a functional overview
- ./e2e - These are the end-to-end tests; they drive the CLI in-process through `cli.Run`
- ./go.mod - This shows which libraries are used, do not add additional third party libraries
- ./architecture - This is a directory with many files explaining the architecture of sub-features. Read the document regarding the feature you wish to know more about.

## Way of work:

- Always write tests first, implementation second
- When fixing a bug, validate the issue with a test, then fix the test
- Keep vendor-specific logic in the vendor package (`internal/vendors/<name>/`). Never add vendor-specific workarounds to generic/shared code like `internal/text/generic/` — the generic layer must remain vendor-agnostic.
- Write all functionality after the asumption of failure, return errors on the failures, write tests which validates errors are returned. Carve out the solution by capturing all things which can go wrong and handling it appropriately.

## Code style:

- Encode any failure in expectation as an error. If findData(path) (data, error) does not find data, that is an error. Encode why in the error. Same goes for any abscence.
- Return types are self-describing. A bare `bool` or a naked `int` beside a value forces the meaning into a comment or into the call site's variable name — return a named type instead: an enum for a classification, a count for a quantity. If a return value needs a comment to explain it, it needs a type.
- Never "log error and return", always return error. This leave a much more testable solution
- If in an async routine, create an error channel passed to the parent who then is responsible to manage the error
- Do not leave bloaty redundant comments. Private functions rarely need any comments at all. Public functions should only describe non-intuitive functionality.
- Make all public functions intuitive via typed return values (including typed errors).
- No reusable component should ever log. Return data should be self descriptive via error sand types, described above.
- If some piece of code is written twice, it should be abstracted
- Never panic in a funcion which returns an error
- Return an error on every failure except in utmost circumstances
- Avoid package level state, always inject dependencies.

## QA Validation

Before signing off on ANY changes, these must all pass:

| Tool        | Command                                                  |
| ----------- | -------------------------------------------------------- |
| Format      | `go run mvdan.cc/gofumpt@latest -w -l .`                 |
| Staticcheck | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` |
| Lint        | `go vet ./...`                                           |
| Test        | `go test ./... -race -cover -count=3 -timeout=30s`       |
| Fix         | `go fix ./...`                                           |
| Dupl        | `go run github.com/mibk/dupl@latest -t 80 .`             |

The dupl check is a signal, not a verdict — see the Duplication policy
below for deciding which clones are acceptable and which need fixing.

**Important:** `go test ./... -race -count=3 -timeout=30s` MUST pass unedited. The strictness
is intentional to produce a highly testable, efficient system which follows strict inversion of control.
Do not modify the timeout, count, or race. Do not add test skips, false-positive tests or any other cheat.
Instead, start testing early and ensure that test passes for each new modification.

**Important:** For new implementations, 70+% test coverage is a must. 90+% test coverage is preferred.

**Every test binary is hermetic:** each test package's `TestMain` is
`func TestMain(m *testing.M) { testenv.Main(m) }`, or `testenv.MainWith(m, testenv.Setup{...})`
when it builds a fixture first (`internal/testenv`). `Main` caps GOMAXPROCS at 2 and sets GOGC to
400 for the binary and its children (on 2026-10-22, 22 uncapped race binaries at full GOMAXPROCS
spent half the gate's CPU in scheduler spin and GC pauses; on 2026-10-28, 53 uncapped binaries
made tests run 5 to 14 times slower than alone, and the collector cost the e2e binary 20.6 CPU-s
at GOGC 100 against 9.0 at 400), points HOME and the XDG directories at a fresh temporary
directory with the go command's own directories pinned, and removes what the developer's shell
could feed a test: `*_API_KEY`, `CLAI_*`, `DEBUG*`, `EDITOR`, `VISUAL`, proxies, `SANDBOX`,
`QWEN_*`, `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE` and `NO_COLOR` go, `TTY` is `/dev/null`
and `LC_ALL` is `C`. A test that needs one of them sets it with `t.Setenv`. The e2e packages use
`e2etest.Main`, which also moves the working directory inside the isolated home, because the
skills discovery walks the working directory's ancestors (on 2026-10-28, five e2e rows run
from the repository read the developer's own skills directory, and one row listed a 1.9 GB
directory of the developer's agent session logs for 240 CPU-s). Race detection is unaffected.

Run `make qa` to run all at once.
