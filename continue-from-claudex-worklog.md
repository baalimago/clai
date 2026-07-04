
## 2026-07-04T11:05:58+0300

### Review notes (implementation vs architecture)

- Feature is implemented (Chat.Source/SourceID, chat index mirroring, discovery + read path via `vendors.SourceReader`, list UI shows foreign rows and clones on continue).
- Dedup behavior after clone exists and is covered by tests (`handler_list_chat_external_clone_test.go`).
- Current vendor implementation targets Claude Code JSONL under `~/.claude/projects/**.jsonl` and injects a system message to preserve provenance.

### Quality

- Good: bounded discovery parsing (`discoverMaxLines`), scanner buffers bumped to avoid 64KB token issues, and tests cover long-line regressions.
- Good: index mirrors `source/source_id` so dedup is O(1) without opening all chat files.
- Concern: `Discover`/`Read` currently ignore ctx cancellation. Not critical, but if repos have many projects/files a cancelled `clai chat list` won’t short-circuit.
- Concern: `filepath.WalkDir` parses *every* jsonl file for `Read` (findSessionFile does a scan up to `discoverMaxLines` per file). OK for now; may need an index later if users have huge histories.

### Maintainability

- SourceReader contract is clean and minimal.
- `internal/chat/handler_list_chat.go` currently hardcodes `allSourceReaders()`; in the future adding readers means editing this list. Acceptable but consider making this vendor-registration based if the set grows.
- Naming: the Claude tool-call normalization helper was overly generic; renamed to clarify vendor specificity.

### Simplicity

- Overall flow matches the arch doc: foreign rows are displayed without import; clone occurs only on continue.
- UI is consistent with existing chat list UX.
- The tool-call normalization logic is somewhat complex but isolated, and has a clear comment block.

### Changes made

1) **Rename** `normalizeToolCallSequence` → `normalizeClaudeToolCallSequence` to avoid suggesting the function is a generic normalizer usable across vendors.
   - Updated call site in `SourceReader.Read`.
   - No behavior change; purely clarity/maintainability.

### Validation

- `go test ./... -race -cover -timeout=10s` ✅
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` ✅
- `go run mvdan.cc/gofumpt@latest -w .` ✅
