# Starter agents

Each file is a clai profile: a prompt, a model, the tools it may call and
the MCP servers it starts. Run one straight from the file, or from the hub:

```bash
clai -prp examples/profiles/reviewer.json q
clai -prp cchub:clai/reviewer q
```

Every MCP package is pinned to an exact version, and no profile holds a
secret: each one reads its keys from the environment variables listed
below. A server declares the ones it needs in `auth.required_env` (or
`auth.token_env` for a hosted server's token), so clai names any that are
missing before it starts the server, and the trust prompt of a `-prp`
profile lists them. Swap `model` for whichever model you have a key for.

| Agent | What it does | Run | Needs |
| --- | --- | --- | --- |
| [reviewer](./reviewer.json) | Reviews a diff from stdin, findings by severity, then a verdict line | `git diff main \| clai -prp cchub:clai/reviewer q` | nothing extra |
| [committer](./committer.json) | Writes a Conventional Commit message for the staged diff | `git diff --cached \| clai -raw -prp cchub:clai/committer q \| git commit -F -` | nothing extra |
| [researcher](./researcher.json) | Searches the web and answers with a short, sourced brief | `clai -prp cchub:clai/researcher q "<question>"` | `uvx` |
| [pr-reviewer](./pr-reviewer.json) | Reviews a GitHub PR and posts the review with inline comments; never approves | `clai -prp cchub:clai/pr-reviewer q owner/repo#123` | `GITHUB_TOKEN` |
| [upgrade-scout](./upgrade-scout.json) | Finds deprecated API use against current library docs and writes a migration plan | `clai -prp cchub:clai/upgrade-scout q "react 18 -> 19"` | `npx`; `CONTEXT7_API_KEY` optional |
| [sql-analyst](./sql-analyst.json) | Answers questions about a Postgres database with SQL, plus index and health advice | `clai -prp cchub:clai/sql-analyst q "<question>"` | `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DATABASE`, `POSTGRES_USER`, `POSTGRES_PASSWORD` (a read-only role), `npx` |
| [site-watch](./site-watch.json) | Hourly headless walk of a site's key flows, reports regressions since the last run | `clai -prp cchub:clai/site-watch q https://example.com "sign-up, pricing"` | `npx`, Google Chrome (`--no-sandbox` as root) |
| [issue-triager](./issue-triager.json) | Labels new issues, links duplicates, asks for missing repro details | `clai -prp cchub:clai/issue-triager q owner/repo dry-run` | `GITHUB_TOKEN`, `npx` |
| [sentry-medic](./sentry-medic.json) | Takes the top new Sentry error, finds the cause and opens a draft fix PR | `clai -prp cchub:clai/sentry-medic q my-org/my-project owner/repo` | `SENTRY_ACCESS_TOKEN`, `GITHUB_TOKEN`, `npx` |
| [trade-bot](./trade-bot.json) | Hourly Polymarket trading with risk limits and a memory of past theses | `clai -prp cchub:clai/trade-bot q Do your hourly run.` | `POLYMARKET_PRIVATE_KEY`, `npx` |

The stateful agents (site-watch, issue-triager, trade-bot) keep their
memory in a small JSONL file in your home directory, named in the
profile's `MEMORY_FILE_PATH`, so they learn across cron runs.

`GITHUB_TOKEN` is used against GitHub's hosted MCP server
(`https://api.githubcopilot.com/mcp/`). A fine-grained token scoped to the
repositories the agent works on is enough: pull requests read and write
for pr-reviewer, issues read and write for issue-triager, contents and
pull requests read and write for sentry-medic.
