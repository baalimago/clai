# Shell completion implementation checklist

## 1. Command surface

- [x] Add visible `completion` command
- [x] Add hidden `__complete` command
- [x] Ensure both commands bypass normal expensive setup/query paths
- [x] Decide where early dispatch should live and document it in code comments

## 2. Completion request/response model

- [x] Define shell-neutral request type
- [x] Include current token semantics
- [x] Include trailing-space semantics
- [x] Define internal completion item type
- [x] Define internal completion kinds: `plain`, `file`, `dir`
- [x] Define MVP stdout response format for hidden command

## 3. Minimal completion metadata

- [x] Add completion-local top-level command metadata
- [x] Add completion-local global flag metadata
- [x] Mark which flags consume values
- [x] Mark value completion behavior per flag
- [x] Confirm actual supported flag spellings match runtime parser behavior

## 4. Engine and parser

- [x] Implement forgiving completion parser
- [x] Handle partial current tokens
- [x] Handle trailing-space state
- [x] Handle unknown commands without errors
- [x] Handle unknown flags without panics
- [x] Detect top-level command context
- [x] Detect `chat` subcommand context
- [x] Detect `tools` command context
- [x] Detect flag-value contexts

## 5. Static completion behavior

- [x] Top-level command completion
- [x] Global flag completion
- [x] `chat` subcommand completion
- [x] Stop structural completion after free-form prompt commands where appropriate

## 6. Dynamic providers

- [x] Add tool-name provider
- [x] Add profile-name provider
- [x] Add shell-context provider
- [x] Make providers lazy
- [x] Memoize provider results per invocation
- [x] Ensure providers use only cheap local discovery

## 7. Dynamic completion behavior

- [x] `tools <tool-name>` suggestions
- [x] `-t/-tools` suggestions
- [x] Support comma-separated tool completion
- [x] Return full replacement token for comma-separated `-t` values
- [x] `-p/-profile` suggestions
- [x] `-asc` suggestions
- [x] Return `file` kind for `-prp`
- [x] Return `dir` kind for directory-valued flags

## 8. Shell output

- [x] Implement `clai completion bash`
- [x] Implement `clai completion zsh`
- [x] Ensure wrappers call hidden `__complete`
- [x] Keep wrappers thin
- [x] Avoid embedding command knowledge in shell scripts
- [x] Handle `plain` completion results
- [x] Handle `file` completion fallback
- [x] Handle `dir` completion fallback

## 9. Tests

- [x] Add engine tests for `clai `
- [x] Add engine tests for `clai -`
- [x] Add engine tests for `clai chat `
- [x] Add engine tests for `clai tools `
- [x] Add engine tests for `clai -t `
- [x] Add engine tests for `clai -t rg,fi`
- [x] Add engine tests for `clai -p pr`
- [x] Add engine tests for `clai -asc mi`
- [x] Add engine tests for `clai q hello`
- [x] Add tests for unknown command behavior
- [x] Add tests for unknown flag behavior
- [x] Add hidden command output-format tests
- [x] Add tests ensuring no stderr noise on successful completion
- [x] Add tests for bash wrapper output
- [x] Add tests for zsh wrapper output
- [x] Run Go tests with `-timeout=30s`

## 10. Optional follow-up

- [ ] Consider `confdir` subpath completion
- [ ] Consider chat ID/index completion
- [ ] Consider model suggestions
- [ ] Consider richer completion protocol if needed
- [ ] Consider extracting shared CLI metadata after MVP stabilizes
- [x] Update README/help text with installation instructions