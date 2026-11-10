# Phase 2 — Remedies

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Expose optional, display-only remedies to CLI and library callers.

## Interface

Written to `pkg/diagnostic/remedy.go`.

```go
package diagnostic

// Remedy is guidance for a user. Command is an example, not an action.
type Remedy struct {
	Instruction string
	Command     []string
}

// Remedier is an error that knows safe, display-only guidance.
type Remedier interface {
	// Remedies returns the safe actions known for this error. It returns no
	// entries when none are known. It never executes a command. (Remedies.1, F8)
	Remedies() []Remedy
}
```

Consumes: Go's `errors.As` and error unwrapping rules.

## Acceptance criteria

| #   | Criterion                                                                                      | Proves         | Proof                  |
| --- | ---------------------------------------------------------------------------------------------- | -------------- | ---------------------- |
| 1   | A caller can inspect safe guidance on a remediable error.                                      | Remedies.1, F8 | `TestRemedierContract` |
| 2   | Errors without known guidance remain usable without a remedy.                                  | Remedies.1     | `TestRemedierContract` |
| 3   | A missing required MCP environment variable gives an actionable instruction without its value. | Remedies.1     | `TestMissingEnvRemedy` |

## Test cases

| Test                   | Level          | Proves      | Given                                            | Then                                                   | Must not                                    |
| ---------------------- | -------------- | ----------- | ------------------------------------------------ | ------------------------------------------------------ | ------------------------------------------- |
| `TestRemedierContract` | contract suite | Remedies.1  | A typed error with guidance or no known guidance | The caller receives safe display data or an empty list | Execute a command or change the error cause |
| `TestMissingEnvRemedy` | contract suite | Criterion 3 | An MCP required-environment error                | Guidance names the missing variable and an action      | Include a credential or its value           |

## Error management

None. No known remedy is an expected state.

## Golden diff

None.

## Removes

None.

## Local facts

Remedies are presentation data, not instructions for automatic execution.
Classification sentinels do not provide remedies.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-11: `Remediable` renamed `Remedier`. |
| start+wip | 1bebb27+wip | `gofumpt; staticcheck ./...; go vet ./...; go test ./... -race -cover -count=3 -timeout=30s; go fix ./...; dupl -t 80 .` | PASS. |
| end+wip   | 1bebb27+wip | `go test ./... -race -cover -count=3 -timeout=30s; gofumpt; dupl -t 80 .; go vet ./...; go fix ./...; staticcheck ./...` | PASS. |
| end+wip   | 1bebb27+wip | `staticcheck ./...; go fix ./...; gofumpt; go test ./... -race -cover -count=3 -timeout=30s; go vet ./...; dupl -t 80 .` | PASS. |

Andon: none.

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good: the Interface matches the written file token for token, and each clause's test names its id.
