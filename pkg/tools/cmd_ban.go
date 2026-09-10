package tools

import (
	"context"
	"fmt"
	"strings"
)

type cmdBanContextKey struct{}

// WithCmdBanContext attaches an immutable command ban policy to a tool-call
// context. The policy is the only ban state there is: every run carries its
// own list on its tool-call context, so distinct policies can run
// concurrently in one process and constructing a querier never alters
// another run's policy (worklog 2026-09-09-conversation-summaries, D29).
// The caller's slice is copied here, so mutating it afterwards never alters
// the installed policy. A context without a policy is permissive.
func WithCmdBanContext(ctx context.Context, entries []string) context.Context {
	return context.WithValue(ctx, cmdBanContextKey{}, append([]string(nil), entries...))
}

// validateCmdNotBannedWithContext refuses a command that matches any entry
// of the policy carried by ctx. For shell execution (args == nil) command is
// the raw freetext string; for direct execution each element of [command] +
// args is tokenized with the same rules as freetext, so sh -c 'git commit'
// (command=sh, args=[-c, git commit]) is caught by entry git commit. Returns
// nil when not banned or when ctx carries no policy.
func validateCmdNotBannedWithContext(ctx context.Context, command string, args []string) error {
	entries, _ := ctx.Value(cmdBanContextKey{}).([]string)
	if len(entries) == 0 {
		return nil
	}
	tokens := tokenizeCommand(command)
	for _, arg := range args {
		tokens = append(tokens, tokenizeCommand(arg)...)
	}
	if matched, banned := matchCmdBanTokens(tokens, entries); banned {
		//lint:ignore ST1005 the refusal is a complete sentence for the agent, not a
		// conventional Go error; the trailing period is part of the contract message.
		return fmt.Errorf("command is banned by policy (matched entry %q). Do not run commands matching this rule.", matched)
	}
	return nil
}

// tokenizeCommand normalizes a raw command string into a flat token list.
// Rules (worklog 2026-08-02-cmd-ban-list, phase 1):
//  1. split the raw command on whitespace into raw tokens;
//  2. strip one leading and one trailing quote character per token,
//     independently, without recursing;
//  3. flattening of quoted multi-word arguments is the emergent result of
//     rules 1-2, not a separate pass;
//  4. split each word on the shell metacharacters ; | & ( ) < > and backtick;
//  5. drop empty tokens.
func tokenizeCommand(raw string) []string {
	tokens := []string{}
	for tok := range strings.FieldsSeq(raw) {
		tok = stripOneQuoteLayer(tok)
		tokens = append(tokens, splitMetachars(tok)...)
	}
	return tokens
}

// matchCmdBan reports whether the command is banned by any entry. Entries are
// tokenized by whitespace split only (empty tokens dropped); the
// tokenization rules above apply to the command string only. The first
// matching entry in list order is returned.
func matchCmdBan(command string, entries []string) (matched string, banned bool) {
	if command == "" || len(entries) == 0 {
		return "", false
	}
	return matchCmdBanTokens(tokenizeCommand(command), entries)
}

// matchCmdBanTokens is the shared matching core: it reports the first entry
// whose tokens appear as a contiguous, in-order run in tokens.
func matchCmdBanTokens(tokens, entries []string) (matched string, banned bool) {
	for _, entry := range entries {
		if containsRun(tokens, strings.Fields(entry)) {
			return entry, true
		}
	}
	return "", false
}

// stripOneQuoteLayer removes one leading quote character and one trailing
// quote character from tok, independently; it never recurses.
func stripOneQuoteLayer(tok string) string {
	if len(tok) > 0 && (tok[0] == '\'' || tok[0] == '"') {
		tok = tok[1:]
	}
	if len(tok) > 0 && (tok[len(tok)-1] == '\'' || tok[len(tok)-1] == '"') {
		tok = tok[:len(tok)-1]
	}
	return tok
}

// splitMetachars splits tok on the shell metacharacters ; | & ( ) < > and
// backtick, dropping any empty parts.
func splitMetachars(tok string) []string {
	var parts []string
	var cur strings.Builder
	for _, r := range tok {
		if strings.ContainsRune(";|&()<>`", r) {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// containsRun reports whether entryTokens appear as a contiguous, in-order
// run anywhere in tokens.
func containsRun(tokens, entryTokens []string) bool {
	if len(entryTokens) == 0 || len(entryTokens) > len(tokens) {
		return false
	}
	for i := 0; i+len(entryTokens) <= len(tokens); i++ {
		matched := true
		for j, entryTok := range entryTokens {
			if tokens[i+j] != entryTok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
