# AGENTS.md

You're working on a project called "clai".

## Always read:

- ./main.go - This contains usage which gives a functional overview
- ./main_test.go - This contains goldenfile tests, which explains functionality in further detail
- ./go.mod - This shows which libraries are used, do not add additional third party libraries
- ./architecture - This is a directory with many files explaining the architecture of sub-features. Read the document regarding the feature you wish to know more about.

## Way of work:

- Always write tests first, implementation second
- When fixing a bug, validate the issue with a test, then fix the test

## Validation:

Before you're done, ensure that these pass:

- go test ./... -race -cover -timeout=10s
- go run honnef.co/go/tools/cmd/staticcheck@latest ./...
- go run mvdan.cc/gofumpt@latest -w .
