package cloudcmd

import (
	"bytes"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/look"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/sync"
)

// Every clai cloud command answers in the same shape: a "▸ subject ·
// detail" head per store or subject, rows under it, and the commands that
// come next as "→ command   why" rows.

func next(command, why string) []string { return []string{"→ " + command, why} }

func listed(paths []string, most int) string {
	if len(paths) <= most {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:most], ", ") + ", +" + count(len(paths)-most) + " more"
}

func firstPullLines(store sync.Store, f sync.FirstPull, s style) []string {
	lines := []string{s.Head(string(store), "first pull on this machine", count(f.Remote)+" "+plural(f.Remote, "file", "files")+" on your account", count(f.Local)+" here")}
	return append(lines, look.Grid("  ", [][]string{
		{"+ " + count(f.OnlyRemote) + " only on your account", "pulled as they are"},
		{"= " + count(f.Same) + " the same", "nothing to do"},
		{"≠ " + count(len(f.Differ)) + " " + plural(len(f.Differ), "differs", "differ"), listed(f.Differ, 3)},
	}, s)...)
}

// firstPullChoices is the question for the files that differ; each answer
// is named by what it keeps, and its letter is that word's first.
func firstPullChoices(store sync.Store, differ int, s style) []string {
	lines := []string{"  Keep which copy of the " + count(differ) + " " + plural(differ, "file that differs?", "files that differ?")}
	return append(lines, look.Grid("  ", [][]string{
		{"[a] your account's", "this machine's goes to cloud/trash/ for 30 days", "same as pull -theirs"},
		{"[h] this machine's", "commit it later to share it", "same as pull -mine"},
		{"[d] decide later", "each file becomes a conflict to resolve"},
		{"[q] quit", "nothing is pulled"},
	}, s)...)
}

func firstPullFlags(store sync.Store, s style) []string {
	return look.Grid("  ", [][]string{
		next("clai cloud pull "+string(store)+" -theirs", "keep your account's copies"),
		next("clai cloud pull "+string(store)+" -mine", "keep this machine's copies"),
	}, s)
}

// choice is an answer to the first pull's question: a mode, or quit.
type choice struct {
	mode sync.Mode
	quit bool
}

func parseChoice(in string) (choice, bool) {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "a", "account":
		return choice{mode: sync.ModeTheirs}, true
	case "h", "here":
		return choice{mode: sync.ModeMine}, true
	case "d", "decide", "later":
		return choice{mode: sync.ModeConflicts}, true
	case "q", "quit":
		return choice{quit: true}, true
	}
	return choice{}, false
}

func conflictLines(paths []string, s style) []string {
	return append(look.Grid("  ", conflictRows(paths), s), look.Grid("  ", conflictNext(paths), s)...)
}

func conflictRows(paths []string) [][]string {
	rows := make([][]string, 0, len(paths))
	for _, p := range paths {
		rows = append(rows, []string{"C " + p, "yours is untouched · your account's copy is cloud/theirs/" + p})
	}
	return rows
}

// conflictNext is the three commands that settle a conflict.
func conflictNext(paths []string) [][]string {
	if len(paths) == 0 {
		return nil
	}
	target := "<path>"
	if len(paths) == 1 {
		target = paths[0]
	}
	return [][]string{
		next("clai cloud diff "+target, "compare the two"),
		next("clai cloud resolve "+target+" -theirs", "take your account's copy"),
		next("clai cloud resolve "+target+" -mine", "keep yours"),
	}
}

func pullLines(res sync.PullResult, s style) []string {
	var parts []string
	if res.Commit > 0 {
		parts = append(parts, fmt.Sprintf("pulled #%d", res.Commit))
	}
	for _, n := range []struct {
		count int
		word  string
	}{{res.Added, "added"}, {res.Updated, "updated"}, {res.Removed, "removed"}, {len(res.Trashed), "replaced"}, {len(res.Merged), "merged"}, {len(res.Forks), "forked"}} {
		if n.count > 0 {
			parts = append(parts, count(n.count)+" "+n.word)
		}
	}
	if n := len(res.Conflicts); n > 0 {
		parts = append(parts, count(n)+" "+plural(n, "conflict", "conflicts"))
	}
	if len(parts) <= 1 {
		parts = append(parts, "up to date")
	}
	lines := []string{s.Head(string(res.Store), parts...)}
	if n := len(res.Merged); n > 0 {
		lines = append(lines, look.Grid("  ", [][]string{{"~ " + count(n) + " merged", "one copy only added messages to the other"}}, s)...)
	}
	var rows [][]string
	for _, f := range res.Forks {
		rows = append(rows, []string{"⑂ " + f.Path, fmt.Sprintf("continued on both machines · the other copy is now %q (%s)", f.Title, f.Forked)})
	}
	for _, p := range res.Linked {
		rows = append(rows, []string{"↪ " + p, "left alone: it reaches through a symlink here"})
	}
	lines = append(lines, look.Grid("  ", rows, s)...)
	if len(res.Trashed) > 0 {
		lines = append(lines, "  "+s.Secondary("replaced files are kept 30 days in cloud/trash/"))
	}
	if len(res.Conflicts) > 0 {
		lines = append(lines, conflictLines(res.Conflicts, s)...)
	}
	return lines
}

func conflictsOpenLines(e *sync.ConflictsOpenError, s style) []string {
	n := len(e.Paths)
	lines := []string{s.Head(string(e.Store), "not committed", count(n)+" "+plural(n, "conflict", "conflicts")+" to resolve first")}
	return append(lines, conflictLines(e.Paths, s)...)
}

func behindLines(e *sync.BehindError, now time.Time, s style) []string {
	n := len(e.Paths)
	lines := []string{s.Head(string(e.Store), "not committed", "your account has "+count(n)+" newer "+plural(n, "file", "files"))}
	rows := make([][]string, 0, n)
	for _, p := range e.Paths {
		row := []string{"~ " + p.Path}
		if p.Commit > 0 {
			detail := fmt.Sprintf("#%d", p.Commit)
			if p.Message != "" {
				detail += fmt.Sprintf(" %q", p.Message)
			}
			detail += " · " + orDash(p.Device)
			if p.UpdatedAt > 0 {
				detail += " · " + ago(now, time.UnixMilli(p.UpdatedAt))
			}
			row = append(row, detail)
		}
		rows = append(rows, row)
	}
	lines = append(lines, look.Grid("  ", rows, s)...)
	return append(lines, look.Grid("  ", [][]string{next("clai cloud pull "+string(e.Store), "then commit again")}, s)...)
}

func commitLines(res sync.CommitResult, s style) []string {
	parts := []string{"nothing to commit"}
	if res.Files > 0 {
		parts = []string{fmt.Sprintf("commit #%d", res.Commit), count(res.Files) + " " + plural(res.Files, "file", "files"), human(res.Bytes)}
		if res.Imported > 0 {
			parts = append(parts, count(res.Imported)+" imported")
		}
	}
	lines := append([]string{s.Head(string(res.Store), parts...)}, look.Grid("  ", excludedRows(res.Excluded), s)...)
	return append(lines, excludedNotes(res.Excluded, s)...)
}

func approveLines(code cloud.DeviceCode, name string, s style) []string {
	return []string{
		s.Head("log in", fmt.Sprintf("approve the code %s for %q", code.UserCode, name)),
		s.Link("  ", code.VerificationURIComplete),
		"  " + s.Secondary("waiting for approval (Ctrl-C to stop)"),
	}
}

func loginLines(account, host string, s style) []string {
	return append([]string{s.Head("logged in", account+" on "+host, "nothing syncs until you ask")}, look.Grid("  ", [][]string{
		next("clai cloud status", "what you would commit"),
		next("clai cloud pull", "bring down your account's config"),
		next("clai cloud commit", "upload your config"),
	}, s)...)
}

func whoamiLines(me cloud.Me, host string, s style) []string {
	lines := []string{s.Head(me.Name(), host, "device "+orDash(me.Device), orDash(me.Access)+" access")}
	names, files, sizes := 0, 0, 0
	for _, st := range sync.Stores {
		info := me.Stores[string(st)]
		names = max(names, utf8.RuneCountInString(string(st)))
		files = max(files, utf8.RuneCountInString(count(info.Files)))
		sizes = max(sizes, utf8.RuneCountInString(human(info.Bytes)))
	}
	for _, st := range sync.Stores {
		info := me.Stores[string(st)]
		detail := right(count(info.Files), files) + " " + plural(info.Files, "file ", "files") + " · " + right(human(info.Bytes), sizes) + " · "
		if info.Commit > 0 {
			detail += fmt.Sprintf("commit #%d", info.Commit)
		} else {
			detail += "no commits yet"
		}
		lines = append(lines, "  "+s.Primary("■")+" "+look.Pad(string(st), names)+"  "+s.Secondary(detail))
	}
	lines = append(lines, "  "+s.Secondary(billingLine(me)))
	if !me.Usage.CardOnFile && me.Usage.BillingOpen && me.Usage.BillingURL != "" {
		lines = append(lines, s.Link("  ", me.Usage.BillingURL))
	}
	return lines
}

func logLines(store sync.Store, entries []sync.LogEntry, now time.Time, s style) []string {
	if len(entries) == 0 {
		return []string{s.Head(string(store), "no commits yet")}
	}
	lines := []string{s.Head(string(store), count(len(entries))+" "+plural(len(entries), "commit", "commits"))}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{fmt.Sprintf("#%d", e.Commit), quoteOr(e.Message), orDash(e.Device) + " · " + count(e.Files) + " " + plural(e.Files, "file", "files") + " · " + ago(now, time.UnixMilli(e.CreatedAt))})
	}
	return append(lines, look.Grid("  ", rows, s)...)
}

// resolution is which copy a resolve kept.
type resolution int

const (
	resolvedEdited resolution = iota
	resolvedMine
	resolvedTheirs
)

func resolveLines(res sync.ResolveResult, how resolution, s style) []string {
	switch how {
	case resolvedTheirs:
		return []string{s.Head(res.Path, "resolved", "took your account's copy", "yours is in cloud/trash/")}
	case resolvedMine:
		return append([]string{s.Head(res.Path, "resolved", "kept yours")}, look.Grid("  ", [][]string{next("clai cloud commit", "share it")}, s)...)
	}
	return append([]string{s.Head(res.Path, "resolved", "kept your edited file")}, look.Grid("  ", [][]string{next("clai cloud commit", "share it")}, s)...)
}

// diffLines heads a file's diff in the shared look and keeps its body as
// diff prints it, so it still pastes as a patch.
func diffLines(d sync.FileDiff, s style) []string {
	if unchanged(d) {
		return []string{s.Head(d.Path, "no changes")}
	}
	var details []string
	if d.Conflict {
		details = append(details, "conflict")
	}
	_, body, _ := strings.Cut(d.Text(), "\n")
	return append([]string{s.Head(d.Path, details...)}, strings.Split(strings.TrimSuffix(body, "\n"), "\n")...)
}

// unchanged is a diff whose every side holds the same bytes.
func unchanged(d sync.FileDiff) bool {
	sides := []sync.Side{d.Mine}
	if d.Theirs != nil {
		sides = append(sides, *d.Theirs)
	}
	for _, side := range sides {
		if side.Missing != d.Base.Missing || !bytes.Equal(side.Bytes, d.Base.Bytes) {
			return false
		}
	}
	return !d.Conflict
}
