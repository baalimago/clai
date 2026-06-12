---
description: Inspect the local repository and summarize useful debugging signals for skill loading experiments.
when_to_use: Use when debugging local skill discovery, trust, activation, or tool-policy behavior in this repository.
arguments:
  - focus
allowed-tools:
  - ls
  - rg
  - cat
  - rows_between
  - git
---

# Local skill debug helper

Investigate the repository for information relevant to `$focus`.

## Procedure

1. Identify the files and directories most relevant to the requested focus.
2. Read only the smallest necessary slices of code or docs.
3. Summarize:
   - what was discovered
   - which files matter
   - what to run next for end-to-end debugging

## Output

Return a concise debugging note with exact file paths and commands when helpful.
