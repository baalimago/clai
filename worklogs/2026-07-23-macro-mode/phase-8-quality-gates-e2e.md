# Phase 8 — Quality gates (e2e migration)

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Ensure the e2e migration (Phases 6–7) passes all standard quality gates and
leaves no dead code or regressions.

## Specification

### Gate commands

```bash
go test ./... -race -cover -timeout=10s
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go run mvdan.cc/gofumpt@latest -w .
go build ./...
```

### Pre-flight checklist

- [x] `internal/chat/handler_list_chat_uat_test.go` deleted
- [x] `internal/chat/handler_list_chat_macro_test.go` deleted
- [x] No unused helper functions in `internal/chat/handler_list_chat_test.go`
  (`seedChatAt`, `seedPeekFixture`, `runPeekScript`, `countPickerPagePrompts` — all removed;
  `useTestSourceReaders` is still in active use across 5 tests, not dead code)
- [x] `main_chat_list_e2e_test.go` created with all Phase 6 tests
- [x] `main_chat_list_e2e_test.go` updated with Phase 7 tests 14–17
- [x] `main_setup_macro_e2e_test.go` created with Phase 7 tests 18–20
- [x] No imports of `internal/chat` test-only symbols from `main` package tests
  (only public API: `chat.Save`, `chat.FromPath`, etc.)

### Regression checks

- [x] All existing e2e tests still pass (no test pollution from new tests)
- [x] Chat package coverage (69.2%) matches post-Phase-6 baseline. The 76.7% → 69.2%
  drop was expected and documented in Phase 6: same paths are tested, but from `main`
  entry points rather than internal package scope.
- [x] No new `staticcheck` warnings
- [x] `gofumpt` produces no diff

## Acceptance criteria

- [x] All four gate commands pass
- [x] No dead code left behind
- [x] No regressions in existing tests en masse

## Design decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D13 | Same gates as Phase 5 | Consistency; these gates have proven effective |
| D14 | Coverage baseline check | Migration should not reduce coverage — the same paths are tested, just from a different entry point |
