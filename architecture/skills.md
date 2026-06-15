# Skills Architecture

This document defines how **skills** work in clai. Skills are prompt-packaged capabilities discovered from well-known directories, parsed from `SKILL.md`, surfaced to the agent through skill descriptors, and loaded into the active context on demand when the feature is explicitly enabled.

The design follows the **Agent Skills progressive-disclosure model** used in pi: the agent is always told which skills exist through compact descriptors, and it requests the full `SKILL.md` content only when a task matches a skill description. The design remains compatible with Claude-style skill directories while fitting clai’s own configuration model, terse UI, and tooling architecture.

Skills are intentionally **opt-in**. clai must remain silent and behaviorally unchanged unless the user enables skills through `skills.json`, a profile, or a CLI flag.

## Scope

The first implementation supports:

- opt-in skill enablement through text config, profiles, and CLI
- skill discovery from configured and conventional directories
- deterministic source precedence and conflict resolution
- parsing `SKILL.md` files with simple frontmatter and markdown body
- progressive-disclosure skill descriptors in agent context
- agent-driven on-demand skill loading based on request and runtime context
- concise log-line UI for discovery and activation
- trust-gated first activation with persisted path+hash approvals
- integration of rendered skill content into the prompt/context for the current run
- per-skill tool allow/deny overrides for the active activation set

The first implementation does **not** support:

- shell preprocessing via `!\`command\``
- hooks
- plugin packaging semantics
- live file watching
- subagent execution for `context: fork`
- non-interactive trust escalation beyond existing cached decisions or `trust_all_skills`

## Skill file format

A skill is a directory containing a required entrypoint:

```text
<skill-dir>/SKILL.md
```

Examples:

```text
~/.config/.clai/skills/review/SKILL.md
/work/repo/agents/skills/review/SKILL.md
/work/repo/.claude/skills/review/SKILL.md
```

Each `SKILL.md` file contains:

1. optional frontmatter delimited by `---`
2. markdown instruction body

Example:

```md
---
description: Review pending changes in the current repository.
arguments: [target]
allowed-tools: Read Grep
---

## Task
Review the changes for $target and summarize risks.
```

The directory name defines the **command/invocation name** of the skill.

## Supported metadata

The parser records the following frontmatter fields from a constrained line-oriented frontmatter format:

| Field | Meaning |
|---|---|
| `name` | Display name |
| `description` | Required routing/summary text for the skill |
| `when_to_use` | Additional usage guidance |
| `argument-hint` | UI hint for arguments |
| `arguments` | Ordered argument names |
| `disable-model-invocation` | Hides the skill from the descriptor block; it is not eligible for automatic loading |
| `user-invocable` | Parsed and stored for compatibility; clai does not rely on manual invocation UI |
| `allowed-tools` | Tools auto-approved for this skill invocation based on existing tool approval system in clai |
| `disallowed-tools` | Tools removed from availability for this skill invocation |
| `model` | Parsed and stored, but not applied in MVP |
| `effort` | Parsed and stored, but not applied in MVP |
| `context` | Parsed and stored; `fork` is not executed in MVP |
| `agent` | Parsed and stored; no subagent execution in MVP |
| `paths` | Parsed and stored for future activation logic |
| `shell` | Parsed and stored; shell execution remains disabled in MVP |

Unknown fields are preserved in parsed metadata for inspection/debugging but have no runtime effect.

The frontmatter contract for MVP is intentionally limited and does not require full YAML compliance. It supports the metadata surface listed below using simple `key: value` lines plus compact list forms already exercised by clai. Shell-style preprocessing and richer YAML constructs such as nested objects, anchors, multiline scalars, or arbitrary type inference are out of scope.

## Discovery roots

clai loads skills from three source classes.

Discovery only runs when skills are enabled for the current run. When skills are disabled, or when enabled discovery finds no valid skills, clai remains silent and does not emit skills setup lines.

## Runtime enablement

Skills are gated by a run-level enablement control rooted in `skills.json.enabled`.

### Default behavior

`enabled` defaults to `false`. Skills are disabled unless explicitly enabled.

### Skills config

`skills.json` contains the default run-level enablement switch:

```json
{
  "enabled": false
}
```

This field is optional. If omitted, clai behaves as though it were `false`.

### Profiles

Profiles may include:

```json
{
  "use_skills": true
}
```

This field is optional. When omitted, a profile inherits the current `skills.json`/runtime value rather than forcing either enablement or disablement.

### CLI flag

clai exposes a string flag following the same parser style as `-t/-tools`:

```text
-s=*        enable skills for the current run
-s=none     disable skills for the current run
(omitted)   no CLI override
```

The long form is:

```text
-skills
```

The effective precedence is:

```text
CLI flag > profile > skills.json.enabled > default(false)
```

The string vocabulary is intentionally narrow in MVP. `*` means enable the subsystem; `none` means disable it. Other values are invalid and should produce a user-facing configuration error rather than being silently reinterpreted.

### Default skills

Default skills are always scanned from the clai config directory:

```text
~/.config/.clai/skills
```

This path is derived from the active clai config directory.

### Global skills

Global skills are configured through a single config file:

```text
~/.config/.clai/skills.json
```

Initial shape:

```json
{
  "globalSkillDirs": ["path/a", "path/b"],
  "projectSkillDirs": ["./agents/skills", ".claude/skills"],
  "trust_all_skills": false,
  "maxActivatedSkills": 10
}
```

`globalSkillDirs` is a list of absolute or user-home-relative directories. Each listed directory is scanned as a root containing `<skill-name>/SKILL.md` children.

`projectSkillDirs` is a list of relative directory patterns, evaluated from the current working directory upward toward the filesystem root. For each ancestor directory, clai checks each configured relative path and scans any existing match as a project skill root.

`trust_all_skills` disables interactive trust prompting and automatically trusts every discovered skill for activation eligibility. Even when this flag is enabled, clai still records the trusted path+hash entries so the trust cache remains warm if the flag is later disabled.

`skills.json` is the only skills-specific configuration file. Additional skills configuration is added to this file in later iterations rather than introducing parallel config files.

### Project skills

Project discovery uses `projectSkillDirs` from `skills.json`.

Default value:

```json
["./agents/skills", ".claude/skills"]
```

Starting from the working directory, clai walks upward toward the filesystem root and, for each ancestor, checks each configured project skill directory. This allows a repository root to define project skills and also allows nested worktrees or subprojects to define local overrides in their own configured project skill directories.

## Source precedence and conflict resolution

When multiple skills share the same invocation name, clai resolves them deterministically by source precedence:

```text
project > global > default
```

Within the `project` class, the nearest directory to the current working directory wins over farther ancestors. This makes nested project-local skills override repository-root skills of the same name.

Within the `global` class, directories are evaluated in the order listed in `globalSkillDirs`. Earlier entries have higher precedence than later entries.

Within the `default` class, there is only one root: `~/.config/.clai/skills`.

A lower-precedence skill with a colliding name is treated as **shadowed** and is not activated or exposed as the canonical skill. Shadowing is surfaced in discovery logging and documented behavior.

## Parsing rules

Each discovered `SKILL.md` is parsed into:

- source class: `default`, `global`, or `project`
- source root path
- skill directory path
- invocation name
- display name
- parsed metadata
- raw markdown body
- normalized body
- parse diagnostics

A skill is **valid** when:

- `SKILL.md` exists
- frontmatter, if present, parses successfully according to the MVP constrained format
- the directory name is non-empty
- the body is non-empty after normalization

A skill is **invalid** when parsing fails or required structural conditions are not met. Invalid skills are skipped and reported in the discovery logs.

## Trust model

A discovered skill is not eligible for activation until it is trusted.

Trust is keyed by:

- the resolved absolute path to the skill directory
- a content hash derived from the skill material used at activation time

At minimum, the hash includes the contents of `SKILL.md`. If later iterations allow supporting files to materially affect activation behavior, those files are added to the hash input as part of the same trust contract.

The trust cache is stored in the clai cache directory:

```text
<clai-cache-dir>/skills_trust.json
```

Each trust record contains:

- skill path
- skill hash
- trust decision metadata sufficient to revalidate activation eligibility

### First activation prompt

When the runtime selects an untrusted skill for activation, clai pauses before activation and prompts the user to decide whether the skill is trusted.

The trust prompt is emitted as a warning using `ancli.Warnf` and should clearly emphasize that an unknown skill is about to run. The message should be cleanly multiline, include concise metadata sufficient for a user decision, and mention that the prompt can be disabled through settings such as `trust_all_skills`.

The trust prompt includes concise metadata sufficient for a user decision:

- skill name
- source class
- resolved path
- current content hash
- a short description summary, if present

If the user approves the skill, clai records the path+hash pair in `skills_trust.json` and proceeds with activation.

If the user rejects the skill, clai does not activate it for the current run and records no positive trust entry for that hash.

### Hash invalidation

If a previously trusted skill changes such that its hash changes, the prior trust record no longer authorizes activation. The next attempted activation triggers the trust prompt again.

This applies equally to default, global, and project skills.

### Trust-all mode

When `trust_all_skills` is `true` in `skills.json`, clai does not prompt interactively before activation. All discovered skills are treated as trusted for selection and activation purposes.

Even in trust-all mode, clai still writes the path+hash entry to `skills_trust.json`. This preserves the trust cache if the user later disables `trust_all_skills`.

### Trust check ordering

The trust check occurs after discovery, parsing, precedence resolution, descriptor injection, and agent request for a specific skill, but before full skill loading, prompt injection, and before any skill-specific tool policy takes effect.

An untrusted skill is never injected into the prompt and never modifies tool availability until trust is established.


## Agent discovery protocol

clai follows the same high-level automatic loading protocol used by pi and the Agent Skills integration model.

1. If skills are enabled for the run, during setup clai discovers valid skills and extracts only lightweight descriptor data:
   - skill name
   - description
   - resolved `SKILL.md` path
   - source class
   - flags relevant to visibility such as `disable-model-invocation`

2. Before the main model request, clai injects a compact **available skills descriptor block** into the agent context. This block lists only model-visible skills and never includes the full body of `SKILL.md`.

3. The agent decides whether a task matches a skill description. When it wants a skill, it calls the internal `load_skill` tool.

4. The runtime trust-checks the selected skill, loads the full skill content, renders substitutions, applies run-local skill policies, and returns the loaded skill content as tool output into the active conversation context.

This is progressive disclosure. Skill descriptions are always cheap and visible; full skill instructions are loaded only when needed through `load_skill`.

### Descriptor block shape

The descriptor block is injected as structured text within the system-side context and follows this shape:

```text
The following skills provide specialized instructions for specific tasks.
Call `load_skill` when the task matches a skill description.
If a skill lists arguments, pass them in the `arguments` field of `load_skill` as the raw user-specific payload for that skill.
If skills are enabled for the run, clai must always include the internal `load_skill` tool in the model-visible tool list, even when user-selected external tools are filtered to a narrower subset.
When a loaded skill references a relative path, resolve it against the skill directory and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>review</name>
    <description>Review pending local changes and highlight risks.</description>
    <arguments>target</arguments>
    <location>/path/to/review/SKILL.md</location>
  </skill>
</available_skills>
```

Only trusted loading reveals the full `SKILL.md` body. The descriptor block itself never grants tool access and does not bypass trust checks. Full skill material enters the conversation only through `load_skill` tool output.

### Visibility rules

A skill with `disable-model-invocation: true` is omitted from the descriptor block and is therefore invisible to automatic loading.

### `load_skill` tool

clai exposes a dedicated internal tool to the agent:

```text
load_skill
```

The tool is part of the agent runtime protocol. It is not a user-managed external tool and does not participate in general filesystem access.

If skills are enabled for the run, `load_skill` is always registered in the model-visible tool list. This remains true even when the user selected a specific subset of external tools with `-t/-tools`.

Minimal argument shape:

```json
{
  "skill": "review"
}
```

If the selected skill descriptor lists arguments, the agent should include them as a raw string payload:

```json
{
  "skill": "review",
  "arguments": "parser regressions"
}
```

`arguments` is optional at the transport layer for robustness, but recommended whenever the descriptor lists argument names.

When `load_skill` is called, the runtime:

1. resolves the requested name against the discovered, precedence-resolved skill set
2. enforces `maxActivatedSkills`
3. evaluates trust for the selected skill path+hash
4. prompts for trust if needed, unless trust has been granted by cache, injected pkg configuration, or `trust_all_skills`
5. loads and renders the trusted skill
6. returns the rendered skill body as tool output
7. applies any run-local skill tool policy

`load_skill` is the only agent-facing mechanism for full skill loading in MVP. Skills are not loaded by asking the agent to read arbitrary skill files directly.


### Activation cap

`skills.json` contains:

```json
{
  "maxActivatedSkills": 10
}
```

Default value:

```json
10
```

If the agent requests more than `maxActivatedSkills` skills in a single run, clai does not load the excess skills. Instead, it appends an error message into context describing that the activation cap was exceeded. The agent may then decide to continue without more skills, abort, or ask the user for help.


## Rendering rules

When a trusted skill is selected for loading, clai renders its body into prompt-ready text.

The renderer supports:

- `$ARGUMENTS`
- `$ARGUMENTS[N]`
- `$0`, `$1`, ...
- `$name` for names declared in `arguments`
- `${CLAUDE_SKILL_DIR}`

The renderer does not execute shell commands and does not expand `!\`command\`` or fenced `\`\`\`!` blocks in MVP. Such text remains literal.

If a skill references a positional or named argument that was not provided, rendering must not fail the run in MVP/beta. Missing references resolve to the empty string. The load may optionally surface a warning, but execution continues.

If `$ARGUMENTS` is not present and activation produced arguments, clai appends a final line in the rendered content:

```text
ARGUMENTS: <original-argument-string>
```

This preserves Claude-style argument propagation while keeping the rendering rules deterministic.

## Activation model

Skills are activated primarily through automatic lookup for a run.
Activation is deterministic and based on discovered skill metadata,
request context, and matching logic in the runtime.

Manual activation may exist as a narrow debugging or development path,
but it is not the primary user workflow and is not a major UI target.

An activated skill contributes:

- rendered instruction content
- source metadata
- argument metadata
- active tool policy overrides

Trusted skill content is injected as additional prompt/context material for the current run only. It does not mutate persisted mode config and it does not permanently alter future sessions.

Multiple skills may be activated in one run. They are applied in the
order chosen by the activation pipeline. Tool overrides are merged in
activation order, then normalized against the base tool selection rules.

Automatic activation should favor relevance over user ceremony. Skills
exist to help the agent do its job better, often by using lookup rules,
descriptions, path hints, and argument-aware rendering that would be
awkward to expose through a manual-only interface.

## Tool policy interaction

Skill tool policy applies only while the skill is active for the current run.

Skills do not load tools. Skill metadata operates only on the tool set already resolved for the current run by clai’s normal setup, discovery, config, profile, and flag pipeline.

`allowed-tools` may auto-approve tools that are already known and already present in the run-resolved tool set.

If a skill requests a tool in `allowed-tools` that exists in clai but is not currently available for the run, clai emits a warning and continues. This makes the degraded behavior visible and signals to the user that the tool must be selected or allowed through the normal clai tool configuration flow.

If a skill requests a tool in `allowed-tools` that is entirely unknown, clai emits a warning and continues.

`disallowed-tools` removes tools from the invocation-level available set, even if those tools were otherwise enabled by config, profile, or command flag.

If multiple active skills conflict:

- the union of all `allowed-tools` is computed
- the union of all `disallowed-tools` is computed
- `disallowed-tools` wins over `allowed-tools`

Tool policy is applied after base tool selection has been computed from the existing clai configuration cascade. Skill tool-policy changes are ephemeral and are discarded when the run ends.

## UI and logging

Skills are surfaced through concise text log lines within clai’s existing output style. clai does not use summary boxes or decorative blocks for skills.

### Discovery logging

After enabled skill discovery completes and at least one valid skill is loaded, clai prints one line per scanned source that produced at least one loaded skill and one line summarizing the loaded result.

Examples:

```text
skills default: ~/.config/.clai/skills [loaded=2]
skills global: /home/user/.claude/skills [loaded=5]
skills global: /opt/company/skills [loaded=3]
skills project: /work/repo/agents/skills [loaded=4]
skills project: /work/repo/.claude/skills [loaded=1]
skills: loaded=9 shadowed=4 invalid=1
```

These lines are emitted during setup in the same general area where tooling and MCP setup information is currently shown. Roots that contain no valid loaded skills are omitted from normal logging to keep the feature quiet by default.

### Activation rendering

Skill activation is rendered with standard ancli/log-style output plus the normal tool-call pretty print already used by clai.

The rendered tool activity includes:

- `load_skill` invocation
- skill name
- source class
- resolved arguments, if any

Canonical visible sequence:

```text
assistant called load_skill(review)
loaded skill review [project]
```

The exact colour/styling follows clai's existing ancli and tool-call rendering. Discovery root summaries, trust prompts, and post-load activation summaries are emitted through ancli. The post-load summary text remains terse and stable and is printed at the moment the runtime attaches a trusted skill to the current run.

## Configuration files and persistence

The skills subsystem introduces one new configuration file:

```text
~/.config/.clai/skills.json
```

Initial contents:

```json
{
  "enabled": false,
  "globalSkillDirs": [],
  "projectSkillDirs": ["./agents/skills", ".claude/skills"],
  "trust_all_skills": false,
  "maxActivatedSkills": 10
}
```

The default skills directory remains conventional and does not require separate configuration:

```text
~/.config/.clai/skills
```

clai setup ensures both of the following exist:

```text
~/.config/.clai/skills.json
~/.config/.clai/skills/
```

If `skills.json` does not exist, clai creates it with the default skills configuration.

Library and package consumers may also provide an injected in-memory skills configuration through `pkg` configuration structs. That injected configuration mirrors `skills.json`, including explicit trust decisions and `trust_all_skills`, and defaults to deny when no trust decision is present.

The skills subsystem also introduces one new cache file:

```text
<clai-cache-dir>/skills_trust.json
```

If `skills_trust.json` does not exist, clai creates it on first trust write.

## Documentation alignment

The skills precedence rule is part of the architectural contract:

```text
nearest project skill > farther project skill > global skill dir order > default config skill
```

The default `projectSkillDirs` includes both `./agents/skills` and `.claude/skills`. This is intentional and part of the default contract. `./agents/skills` satisfies the project-native convention; `.claude/skills` preserves compatibility with externally authored Claude-style skills.

## Acceptance criteria

The skills subsystem is complete when all of the following are true:

1. clai discovers skills from:
   - only when skills are enabled for the run
   - `~/.config/.clai/skills`
   - every directory listed in `~/.config/.clai/skills.json.globalSkillDirs`
   - every relative project directory listed in `~/.config/.clai/skills.json.projectSkillDirs`, resolved from the current directory upward

2. clai parses `SKILL.md` files with constrained frontmatter and markdown body and exposes:
   - invocation name from directory name
   - body content
   - supported metadata fields
   - source classification and source path

3. when skills are enabled and at least one valid skill is discovered, clai injects an available-skills descriptor block into agent context containing skill name, description, location, and declared argument names for all model-visible skills.

4. the agent may request full loading of a skill from that descriptor block by calling the internal `load_skill` tool, and clai performs loading on demand without requiring a manual invocation UI.

5. clai prompts the user to trust a skill before first full load of a given path+hash pair, unless trust is granted through injected `pkg` configuration or `trust_all_skills` is enabled. The prompt is a warning-style, cleanly formatted multiline message that emphasizes the skill is untrusted and notes that the check can be disabled in settings.

6. conflicts resolve deterministically with precedence:
   - nearer project over farther project
   - project over global
   - earlier global directory over later global directory
   - global over default

7. when enabled discovery loads at least one valid skill, clai prints concise line-oriented logs that include:
   - each scanned source path that contributed at least one canonical loaded skill
   - loaded counts per source after precedence resolution
   - total loaded, shadowed, and invalid counts

7a. when skills are disabled, or when enabled discovery finds no valid skills, clai remains silent and prints no skills setup lines.

8. skill loading is visible through normal tool-call rendering for the internal `load_skill` tool and prints concise ancli post-load lines containing:
   - skill name
   - skill source class
   - resolved arguments when present

9. activated skills render argument substitutions correctly for:
   - `$ARGUMENTS`
   - `$ARGUMENTS[N]`
   - `$0`, `$1`, ...
   - `$name`
   - `${CLAUDE_SKILL_DIR}`

10. missing positional or named argument references do not fail activation in MVP/beta; they resolve to the empty string so the run continues even if the model omitted `load_skill.arguments`.

11. only trusted, agent-requested skill content is injected into the current run’s prompt/context without mutating persistent mode config.

12. `allowed-tools` and `disallowed-tools` operate only on the tool set already resolved for the current run; skills do not load tools, unavailable or unknown requested tools produce warnings and degraded continuation, and all skill tool-policy effects are discarded when the run ends.

13. shell preprocessing syntax remains disabled and unexecuted in MVP.

14. a trusted skill becomes untrusted for loading if its stored hash no longer matches the current content hash.

15. when `trust_all_skills` is enabled, clai still stores path+hash trust records for loaded skills.

16. if a run exceeds `maxActivatedSkills`, clai appends an error to context and does not load additional skills beyond the cap.

17. the architecture docs index references `architecture/skills.md` as the authoritative design note for the subsystem.
