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

## Validation:

Before implementing any change, run this to establish the duplication baseline:

```bash
go run github.com/mibk/dupl@latest -t 80 .
```

When done, run the full QA suite:

```bash
make qa
```

Then re-run dupl to ensure no needless code duplication was added:

```bash
go run github.com/mibk/dupl@latest -t 80 .
```

ALWAYS TEST WITH TIMEOUT. Otherwise you may deadlock debugging efforts.
