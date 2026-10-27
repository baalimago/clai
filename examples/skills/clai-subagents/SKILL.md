---
name: clai-subagents
description: How to run clai subagents (workers) in parallel with async_cmd, then collect, check and merge their answers. Load it before starting any clai worker.
---

# Running clai subagents with async_cmd

A worker is a separate `clai` process. You start it with `async_cmd`, it runs in parallel with you and with other workers, and its stdout is its answer. A worker sees nothing of your conversation: everything it needs goes into its brief.

## 1. Learn the flags first

Before your first worker, start `clai help` and `clai q -h` the same way: `async_cmd` with command `clai` and args `["help"]`, then `["q", "-h"]`. Await both with `async_cmd_await` and read them with `async_cmd_logs`. The flags change between versions, so trust that output over this page.

## 2. Start a worker

`async_cmd` takes `command`, `args` (a list, one argument per item), and optionally `cwd` and `env`. It returns an `async_cmd_id` straight away.

The command is always `clai`. The args put the flags first, then `q`, then the brief as one single item, which may span many lines:

- A worker with chosen tools: `["-ro", "-summarize=false", "-cm", "<model>", "-t", "<tool,tool,...>", "q", "<brief>"]`.
- A worker that is a Hub agent: `["-ro", "-summarize=false", "-prp", "cchub:<handle>/<agent>@<version>#sha256=<hex>", "q", "<brief>"]`.
- A worker that is a local profile: `["-ro", "-summarize=false", "-p", "<profile name>", "q", "<brief>"]`.

The flags do these things:
- `-ro` (reply only) keeps the worker's tool calls, tool output and reasoning on stderr, so stdout is its answer alone. Always pass it.
- `-summarize=false` skips the conversation summary, which a worker does not need.
- `-t` gives the worker only the tools it needs. A read-only worker gets read tools, such as `git,rg,cat,rows_between,ls,find`. Give write tools only to a worker whose piece must edit files, and never let two workers edit the same file.
- `-cm` picks the model. Use your own model unless the task says otherwise.

A Hub agent that is not pinned with `#sha256=`, and that this machine has not trusted, is refused with exit code 3, because there is no terminal to ask in. Pin it, or do that piece yourself.

## 3. Write the brief

The brief is the only thing the worker knows, so it holds all of this:
- The goal, in one or two sentences, and why it matters.
- The exact scope: which files, directories, refs, ids or time range. Give the commands or tool calls you used to find them, so the worker sees the same thing.
- What it must not do or touch.
- The exact output format you want back. Ask for it between marker lines, such as `BEGIN FINDINGS` and `END FINDINGS`, with a fixed line shape and `none` when there is nothing. Then you can take the answer out of the stdout reliably.
- Evidence rules: cite path:line, ids or links, and never guess.

## 4. Run them in parallel

1. Start every worker of a round before you await any of them. Keep it to at most 8 at once.
2. Call `async_cmd_await` once with all their ids and a `timeout_seconds` that fits the work (600 to 1800 for a review or a research piece).
3. Read each answer with `async_cmd_logs`: the stdout preview is its answer. A preview marked truncated has the full text in `log_path`, so read it with `cat`. A failed worker's stderr says why.
4. A worker that is still running at the timeout gets `async_cmd_cancel`; do its piece yourself or rerun it with a smaller scope. Check one with `async_cmd_status` without waiting.

## 5. Check and merge

- Treat a worker's answer as a claim. Before you keep it, check one concrete point of each answer against the source yourself: open the cited line, rerun the query, read the link.
- Rerun a worker once when its answer is missing the markers, wrong or incomplete, with a sharper brief. If it fails again, do the piece yourself and say so.
- Merge duplicates, keep the clearest wording, and report how many workers ran and how many answers you dropped.

## cmd-ban

A profile's `cmd-ban` entries (for example `git push`, `rm`) also apply to what `async_cmd` starts, and the worker inherits them. Your brief, the prompt of a direct `clai` call, is not matched against them. If a worker is still refused as banned, look at the matched entry in the error: it is in one of the flags or tool names, not in the brief. Fix that argument, and never work around a ban by any other means.

## Rules

- Use `async_cmd` only to run `clai`, unless your own instructions name another program.
- Never claim a worker ran, or what it found, without having read its output.
