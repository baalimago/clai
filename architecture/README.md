# Architecture docs index

This directory contains short design notes for key parts of **clai**. Each file focuses on one command or subsystem and points to the relevant implementation modules.

## Core concepts

- **[config.md](./config.md)** — Where config lives (config/cache dirs), what files exist (mode configs, vendor model configs, profiles, conversations), and the override cascade (defaults → file → profiles → flags).
- **[chat.md](./chat.md)** — Conversation storage format, global previous-query (`globalScope.json`), directory-scoped reply bindings, and the `clai chat continue` flow.
- **[continue-from-claudex.md](./continue-from-claudex.md)** — Auto-discover and continue conversations from external AI tools (Claude Desktop/Code, Codex, Pi, Cursor, …) directly from `clai chat list`. Foreign conversations are inspected on-the-fly and cloned to native clai chats on continue, with a shared `source` field that also enables chat forking.
- **[chat-groups.md](./chat-groups.md)** — Entry-message clustering in `clai chat list`: conversations with the same first user message are collapsed into `[group:N]` rows; selecting a group expands it to show member conversations. Uses a content-derived `GroupKey` (hex SHA-256) that parallels the `Source`/`SourceID` identity pattern. Zero changes to `SelectFromTable`.
- **[dirscope.md](./dirscope.md)** — Directory bindings, per-directory conversation history, origin-directory stamping, and conversation search: the `sha256`-keyed `version: 2` binding record (`abs_path`, timestamped `history`), always-on recording + `origin_dir` stamping, in-place `version: 1 → 2` upgrade, the opt-in (`-lb/-lookback`) lookback (recent-conversations descriptor + directory-anchored `search_conversations`, plus `inspect_conversation` / `read_message` for granular reads, brute-force with a documented index threshold), and the `[d]ir` toggle filter in `clai chat list`.
- **[streaming.md](./streaming.md)** — How vendor streaming is normalized into a common event stream and consumed by the querier (text deltas, tool calls, stop events, errors).
- **[tooling.md](./tooling.md)** — Tool registry + allow-list selection (`-t/-tools`), tool-call execution loop, and MCP server integration.
- **[skills.md](./skills.md)** — Skill discovery, parsing, precedence, rendering, activation logging, and invocation-scoped tool policy.

## Command docs

- **[query.md](./query.md)** — Primary `clai query` command: flag/config application, prompt assembly (stdin + `{}` replacement), tool-call recursion, and persistence.
- **[replay.md](./replay.md)** — `clai replay` / `clai re` (global) and relationship to dir-scoped replay.
- **[dre.md](./dre.md)** — `clai dre`: prints the last message from the directory-bound conversation for the current working directory.
- **[photo.md](./photo.md)** — `clai photo`: image generation flow, prompt formatting, vendor routing, and output modes (local/url).
- **[video.md](./video.md)** — `clai video`: video generation (OpenAI Sora), optional image-to-video prompt parsing, and output modes.
- **[tools.md](./tools.md)** — `clai tools` inspection UI: list tools and print JSON schema for one tool.
- **[profiles.md](./profiles.md)** — `clai profiles`: lists profile JSONs and prints a small summary; profiles are applied via `-p` flags (see CONFIG).
- **[setup.md](./setup.md)** — `clai setup` interactive wizard for editing mode configs, vendor model files, profiles, and MCP server configs.
- **[help.md](./help.md)** — `clai help` command: prints usage template plus special handling for `help profile`.
- **[version.md](./version.md)** — `clai version`: prints build/module version information and exits.

## UI / output

- **[colours.md](./colours.md)** — ANSI theming via `<clai-config>/theme.json` and `NO_COLOR` disable behavior; where colour is applied (pretty print, tables, obfuscated previews).

## Reading order suggestions

- If you’re new: start with **config.md → query.md → chat.md**.
- If you’re working on tool calls: **tooling.md → query.md → streaming.md**.
- If you’re debugging reply context: **chat.md → replay.md → dre.md**.
- If you’re working on chat listing / grouping: **chat.md → continue-from-claudex.md → chat-groups.md → dirscope.md**.
