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
