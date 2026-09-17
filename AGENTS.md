# AGENTS.md

You're working on a project called "clai".

## Always read:

- ./main.go - This contains usage which gives a functional overview
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

Run `make qa` to run all at once.
