# Starter agents

Each file is a clai profile: a prompt, a model, the tools it may call and
the MCP servers it starts. Run one straight from the file, or from the hub:

```bash
clai -prp examples/profiles/reviewer.json -qs q
clai -prp cchub:clai/reviewer -qs q
```

An agent run with no prompt and nothing piped in acts on its defaults:
reviewer reviews your branch, committer reads the staged diff. reviewer,
pr-reviewer and upgrade-scout split their work across parallel clai
workers: each runs `clai` through `async_cmd`, a separate process with its
own brief. How to run them is the
[clai-subagents](../skills/clai-subagents/SKILL.md) skill
(`cchub:clai/clai-subagents`), which these profiles name in `"skills"` and
load before their first worker; your own orchestrating profile can do the
same.

Every MCP package is pinned to an exact version, and no profile holds a
secret: each one reads its keys from the environment variables listed
below. A server declares the ones it needs in `auth.required_env` (or
`auth.token_env` for a hosted server's token), so clai names any that are
missing before it starts the server, and the trust prompt of a `-prp`
profile lists them. Swap `model` for whichever model you have a key for.

| Agent | What it does | Run | Needs |
| --- | --- | --- | --- |
| [reviewer](./reviewer.json) | Reviews your branch against the default branch (plus uncommitted work) with parallel reviewer workers, verifies every finding (library behaviour against Context7 docs, no key needed), then prints a verdict; bans `git push` and `rm` | `clai -prp cchub:clai/reviewer -qs q` | nothing extra |
| [committer](./committer.json) | Writes the commit message for the staged changes, in the repository's own style | `clai -ro -prp cchub:clai/committer q \| git commit -F -` | nothing extra |
| [researcher](./researcher.json) | Searches the web and answers with a short, sourced brief; with no question, what changed lately for your project's main dependencies | `clai -prp cchub:clai/researcher -qs q` | `uvx` |
| [pr-reviewer](./pr-reviewer.json) | Finds the current branch's PR, runs reviewer on it and posts the review with inline comments; never approves | `clai -prp cchub:clai/pr-reviewer -qs q` | `GITHUB_TOKEN` |
| [upgrade-scout](./upgrade-scout.json) | Finds deprecated API use against current library docs and writes a migration plan, one worker per library | `clai -prp cchub:clai/upgrade-scout -qs q` | `npx`; `CONTEXT7_API_KEY` optional |
| [sql-analyst](./sql-analyst.json) | Answers questions about a Postgres database with SQL, plus index and health advice; with no question, a check-up | `clai -prp cchub:clai/sql-analyst -qs q` | `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DATABASE`, `POSTGRES_USER`, `POSTGRES_PASSWORD` (a read-only role), `npx` |
| [site-watch](./site-watch.json) | Hourly headless walk of a site's key flows, reports regressions since the last run | `clai -prp cchub:clai/site-watch -qs q https://www.clai.chat "docs search, hub, pricing"` | `npx`, Google Chrome (`--no-sandbox` as root) |
| [issue-triager](./issue-triager.json) | Labels new issues, links duplicates, asks for missing repro details | `clai -prp cchub:clai/issue-triager -qs q dry-run` (in a clone) | `GITHUB_TOKEN`, `npx` |
| [sentry-medic](./sentry-medic.json) | Takes the top new Sentry error of the repo you are in, finds the cause and opens a draft fix PR | `clai -prp cchub:clai/sentry-medic -qs q` (in a clone) | `GITHUB_TOKEN`; the first run in a terminal opens your browser to sign in to Sentry's hosted MCP server |
| [trade-bot](./trade-bot.json) | Hourly Polymarket trading with risk limits and a memory of past theses | `clai -prp cchub:clai/trade-bot -qs q Do your hourly run.` | `POLYMARKET_PRIVATE_KEY`, `npx` |

The stateful agents (site-watch, issue-triager, trade-bot) keep their
memory in a small JSONL file in your home directory, named in the
profile's `MEMORY_FILE_PATH`, so they learn across cron runs.

`GITHUB_TOKEN` is used against GitHub's hosted MCP server
(`https://api.githubcopilot.com/mcp/`). A fine-grained token scoped to the
repositories the agent works on is enough: pull requests read and write
for pr-reviewer, issues read and write for issue-triager, contents and
pull requests read and write for sentry-medic.
