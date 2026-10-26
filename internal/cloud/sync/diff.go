package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Side is one labelled version of a file in a diff.
type Side struct {
	Label string
	Bytes []byte
	// Missing: the file does not exist on this side.
	Missing bool
}

// FileDiff compares versions of one file: base against mine, and, for a
// conflict or a --remote diff, against theirs too.
type FileDiff struct {
	Path     string
	Conflict bool
	Base     Side
	Mine     Side
	Theirs   *Side
}

// Text renders the diff as unified hunks; a conversation is summarised by
// its message counts, since a one-line JSON diff says nothing.
func (d FileDiff) Text() string {
	var b strings.Builder
	head := d.Path
	if d.Conflict {
		head += "  conflict"
	}
	b.WriteString(head + "\n")
	pairs := []Side{d.Mine}
	if d.Theirs != nil {
		pairs = append(pairs, *d.Theirs)
	}
	for _, side := range pairs {
		if chatFile.MatchString(d.Path) {
			fmt.Fprintf(&b, "%s: %s; %s: %s\n", d.Base.Label, describeChat(d.Base), side.Label, describeChat(side))
			continue
		}
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", d.Base.Label, side.Label)
		b.WriteString(unifiedHunks(splitLines(d.Base), splitLines(side), 3))
	}
	return b.String()
}

func splitLines(s Side) []string {
	if s.Missing || len(s.Bytes) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(s.Bytes), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func describeChat(s Side) string {
	if s.Missing {
		return "absent"
	}
	var c chatShape
	if json.Unmarshal(s.Bytes, &c) != nil {
		return "not a conversation"
	}
	return fmt.Sprintf("%d messages", len(c.Messages))
}

// Diff compares one path. Without remote it shows this machine's change
// against the base (and the server's side of a conflict); with remote it
// shows the server's current copy against this machine's.
func (e *Engine) Diff(ctx context.Context, rel string, remote bool) (FileDiff, error) {
	store, ok := StoreOf(rel)
	if !ok {
		return FileDiff{}, &UnknownPathError{Path: rel}
	}
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return FileDiff{}, err
	}
	d := FileDiff{Path: rel}
	base, hasBase := st.Entries[rel]
	d.Base = Side{Label: "base", Missing: !hasBase || base.Deleted}
	if !d.Base.Missing {
		d.Base.Label = fmt.Sprintf("base   (rev %d)", base.Rev)
		if d.Base.Bytes, err = e.baseBytes(ctx, store, rel, base); err != nil {
			return d, err
		}
	}
	d.Mine = Side{Label: "mine   (this machine)"}
	if d.Mine.Bytes, err = os.ReadFile(e.abs(rel)); errors.Is(err, fs.ErrNotExist) {
		d.Mine.Missing, err = true, nil
	}
	if err != nil {
		return d, err
	}
	if c, ok := st.Conflicts[rel]; ok {
		d.Conflict = true
		theirs := Side{Label: fmt.Sprintf("theirs (#%d, %s)", c.Commit, orDash(c.Device)), Missing: c.Deleted}
		if !c.Deleted {
			if theirs.Bytes, err = os.ReadFile(TheirsPath(e.ConfDir, rel)); err != nil {
				return d, err
			}
		}
		d.Theirs = &theirs
		return d, nil
	}
	if !remote {
		return d, nil
	}
	entries, _, err := e.api().allChanges(ctx, store, st.Cursor)
	if err != nil {
		return d, err
	}
	theirs := Side{Label: "theirs (the server, same as base)", Bytes: d.Base.Bytes, Missing: d.Base.Missing}
	for _, r := range entries {
		if r.Path != rel || (hasBase && base.Rev >= r.Rev) {
			continue
		}
		theirs = Side{Label: fmt.Sprintf("theirs (#%d, %s)", r.Commit, orDash(r.Device)), Missing: r.Deleted}
		if !r.Deleted {
			if theirs.Bytes, err = e.fetch(ctx, rel, r.SHA256); err != nil {
				return d, err
			}
		}
	}
	d.Theirs = &theirs
	return d, nil
}

// baseBytes reads the local base copy of a config file, else asks the
// server for the base's blob.
func (e *Engine) baseBytes(ctx context.Context, store Store, rel string, base baseEntry) ([]byte, error) {
	if store == StoreConfig {
		if b, err := os.ReadFile(baseCopy(e.ConfDir, rel)); err == nil && hashBytes(b) == base.SHA256 {
			return b, nil
		}
	}
	return e.fetch(ctx, rel, base.SHA256)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// unifiedHunks renders a line diff of a and b with context lines around
// each change. Files too large for the line table are only reported as
// differing.
func unifiedHunks(a, b []string, context int) string {
	if len(a)*len(b) > maxDiffCells {
		return "(too large to diff line by line)\n"
	}
	ops := lineOps(a, b)
	var out strings.Builder
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-context)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*context || run == len(ops) {
				end = min(len(ops), end+context)
				break
			}
			end = run
		}
		aStart, bStart := ops[start].ai+1, ops[start].bi+1
		aCount, bCount := 0, 0
		for _, op := range ops[start:end] {
			if op.kind != '+' {
				aCount++
			}
			if op.kind != '-' {
				bCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		for _, op := range ops[start:end] {
			line := op.text
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			out.WriteString(string(op.kind) + line)
		}
		i = end
	}
	return out.String()
}

const maxDiffCells = 4_000_000

type lineOp struct {
	kind   byte
	text   string
	ai, bi int
}

// lineOps is a longest-common-subsequence edit script.
func lineOps(a, b []string) []lineOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []lineOp
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, lineOp{' ', a[i], i, j})
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, lineOp{'+', b[j], i, j})
			j++
		default:
			ops = append(ops, lineOp{'-', a[i], i, j})
			i++
		}
	}
	return ops
}
