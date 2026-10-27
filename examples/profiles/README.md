# Starter agents

Each file is a clai profile: a prompt, a model, the tools it may call and
the MCP servers it starts. Run one straight from the file, or from the hub:

```bash
clai -profile-path examples/profiles/reviewer.json -query-summary query
clai -profile-path cchub:clai/reviewer -query-summary query
```

An agent run with no prompt and nothing piped in acts on its defaults:
reviewer reviews your branch, pr-summarizer sums it up, cloud-medic checks
the service whose repo you are in, project-planner plans its next two
weeks, and researcher reports what changed for its dependencies. All but
project-planner split their work across parallel clai workers: each runs
`clai` through `async_cmd`, a separate process with its own brief. How to
run them is the [clai-subagents](../skills/clai-subagents/SKILL.md) skill
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
| [reviewer](./reviewer.json) | Reviews your branch against the default branch (plus uncommitted work) with parallel reviewer workers, verifies every finding (library behaviour against Context7 docs, no key needed), then prints a verdict; bans `git push` and `rm` | `clai -profile-path cchub:clai/reviewer -query-summary query` | nothing extra |
| [pr-summarizer](./pr-summarizer.json) | Investigates your whole branch (parallel reader workers when it is large) and writes the one commit message that sums it up: a subject of at most 72 characters, a body of at most two paragraphs and 200 words; bans `git push` and `git commit` | `clai -reply-only -profile-path cchub:clai/pr-summarizer query` | nothing extra |
| [cloud-medic](./cloud-medic.json) | Works out where the service of the repo you are in runs and reports errors (Sentry, GitHub Actions, and kubectl, wrangler, fly, gcloud or aws when signed in), finds the root cause of the worst with investigator workers and opens a draft fix PR; keeps a status note in slivingdoc; bans every deploying or deleting command | `clai -profile-path cchub:clai/cloud-medic -query-summary query` (in a clone) | `GITHUB_TOKEN`, `npx`; the first run in a terminal opens your browser to sign in to Sentry |
| [project-planner](./project-planner.json) | Finds where the work is tracked (GitHub, Linear when the repo uses it, roadmap files, git history), checks how the last plan went and plans the next two weeks; keeps the plan in slivingdoc; never edits an issue | `clai -profile-path cchub:clai/project-planner -query-summary query` (in a clone) | `GITHUB_TOKEN`, `npx`; Linear signs in through your browser on first use |
| [researcher](./researcher.json) | Deep research: splits the question, searches each part, parallel reader workers, every claim checked against its source; keeps its reports in slivingdoc; with no question, what changed for your project's dependencies since the last report | `clai -profile-path cchub:clai/researcher -query-summary query` | `uvx`, `npx` |

cloud-medic, project-planner and researcher keep their memory in
[slivingdoc](https://github.com/baalimago/slivingdoc), a shared text
notebook served over MCP, each under its own path (`clai/cloud-medic`,
`clai/project-planner`, `clai/researcher`, which its `--writable-paths`
enforces). The first run in a terminal signs you in to slivingdoc (or set
`SLIVINGDOC_TOKEN`). Where it cannot sign in, such as a cron run before
the first sign-in, they run and say that memory is off, since the server
is marked `"optional": true`.

`GITHUB_TOKEN` is used against GitHub's hosted MCP server
(`https://api.githubcopilot.com/mcp/`). A fine-grained token scoped to the
repositories the agent works on is enough: contents and pull requests read
and write for cloud-medic, issues and pull requests read for
project-planner.

## More examples

[cody](./cody.json), [gopher](./gopher.json) and [ops](./ops.json) are
the profiles clai started with; ops expects MCP servers you configure. [trade-bot](./trade-bot.json) trades Polymarket
hourly with risk limits and keeps a memory of past theses in a JSONL file
named in its `MEMORY_FILE_PATH`; run it with
`clai -profile-path gh:baalimago/clai/examples/profiles/trade-bot.json -query-summary query Do your hourly run.`
(needs `POLYMARKET_PRIVATE_KEY` and `npx`).
