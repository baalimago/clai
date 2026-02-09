# Architecture docs index

This directory contains short design notes for key parts of **clai**. Each file focuses on one command or subsystem and points to the relevant implementation modules.

## Core concepts

- **[CONFIG.md](./CONFIG.md)** — Where config lives (config/cache dirs), what files exist (mode configs, vendor model configs, profiles, conversations), and the override cascade (defaults → file → profiles → flags).
- **[CHAT.md](./CHAT.md)** — Conversation storage format, global previous-query (`globalScope.json`), directory-scoped reply bindings, and the `clai chat continue` flow.
- **[STREAMING.md](./STREAMING.md)** — How vendor streaming is normalized into a common event stream and consumed by the querier (text deltas, tool calls, stop events, errors).
- **[TOOLING.md](./TOOLING.md)** — Tool registry + allow-list selection (`-t/-tools`), tool-call execution loop, and MCP server integration.

## Command docs

- **[QUERY.md](./QUERY.md)** — Primary `clai query` command: flag/config application, prompt assembly (stdin + `{}` replacement), tool-call recursion, and persistence.
- **[REPLAY.md](./REPLAY.md)** — `clai replay` / `clai re` (global) and relationship to dir-scoped replay.
- **[DRE.md](./DRE.md)** — `clai dre`: prints the last message from the directory-bound conversation for the current working directory.
- **[PHOTO.md](./PHOTO.md)** — `clai photo`: image generation flow, prompt formatting, vendor routing, and output modes (local/url).
- **[VIDEO.md](./VIDEO.md)** — `clai video`: video generation (OpenAI Sora), optional image-to-video prompt parsing, and output modes.
- **[TOOLS.md](./TOOLS.md)** — `clai tools` inspection UI: list tools and print JSON schema for one tool.
- **[PROFILES.md](./PROFILES.md)** — `clai profiles`: lists profile JSONs and prints a small summary; profiles are applied via `-p` flags (see CONFIG).
- **[SETUP.md](./SETUP.md)** — `clai setup` interactive wizard for editing mode configs, vendor model files, profiles, and MCP server configs.
- **[HELP.md](./HELP.md)** — `clai help` command: prints usage template plus special handling for `help profile`.
- **[VERSION.md](./VERSION.md)** — `clai version`: prints build/module version information and exits.

## UI / output

- **[COLOURS.md](./COLOURS.md)** — ANSI theming via `<clai-config>/theme.json` and `NO_COLOR` disable behavior; where colour is applied (pretty print, tables, obfuscated previews).

## Reading order suggestions

- If you’re new: start with **CONFIG.md → QUERY.md → CHAT.md**.
- If you’re working on tool calls: **TOOLING.md → QUERY.md → STREAMING.md**.
- If you’re debugging reply context: **CHAT.md → REPLAY.md → DRE.md**.
