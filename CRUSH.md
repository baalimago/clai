# CRUSH.md - Development Guide for clai

## Build/Test/Lint Commands
```bash
# Build the project
go build .

# Run all tests
go test ./...

# Run tests for a specific package
go test ./internal/chat

# Run a specific test
go test -run TestSaveAndFromPath ./internal/chat

# Run tests with coverage
go test -cover ./...

# Format code
go fmt ./...

# Lint (uses staticcheck via CI)
staticcheck ./...

# Install and run locally
go install . && clai help
```

## Code Style Guidelines

**Imports**: Use standard library first, then external packages, then internal packages with `pub_models` alias for public models
**Naming**: Use camelCase for variables, PascalCase for exported types, snake_case for JSON tags
**Error Handling**: Always wrap errors with context using `fmt.Errorf("description: %w", err)`
**Types**: Prefer explicit types, use struct tags for JSON serialization
**Testing**: Use table-driven tests, `t.TempDir()` for temp directories, `reflect.DeepEqual` for comparisons
**Packages**: Internal packages under `internal/`, public API under `pkg/`
**Dependencies**: Uses `go_away_boilerplate` for common utilities, avoid adding new external deps without review