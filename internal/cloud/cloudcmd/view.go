package cloudcmd

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

const (
	// listLimit is the most changes status lists one by one; more are grouped.
	listLimit = 10
	// maxGroups is the most group rows, the folded row included.
	maxGroups = 6
	peekItems = 3
	peekName  = 16
	lineWidth = 100
)

// style draws clai's look: the ▸ marker in the theme's primary colour and
// details in its secondary one, or plain text when color is off.
type style struct{ color bool }

func (s style) paint(c, t string) string {
	if !s.color || c == "" || t == "" {
		return t
	}
	return table.Colorize(c, t)
}

func (s style) primary(t string) string   { return s.paint(utils.TableTheme().Primary, t) }
func (s style) secondary(t string) string { return s.paint(utils.TableTheme().Secondary, t) }

// head is "▸ <title> · <detail> · <detail>".
func (s style) head(title string, details ...string) string {
	line := s.primary("▸") + " " + title
	if len(details) > 0 {
		line += s.secondary(" · " + strings.Join(details, " · "))
	}
	return line
}

func (s style) link(indent, target string) string {
	return indent + s.primary("→") + " " + target
}

func statusLines(st sync.Status, s style) []string {
	lines := []string{s.head(string(st.Store), statusDetails(st)...)}
	for _, p := range st.Conflicts {
		lines = append(lines, "  C "+p, s.link("    ", "clai cloud resolve "+p+" -mine|-theirs"))
	}
	if len(st.Changes) <= listLimit {
		lines = append(lines, changeList(st, s)...)
	} else {
		lines = append(lines, changeGroups(st, s)...)
	}
	return append(lines, excludedLines(st.Excluded, s)...)
}

func statusDetails(st sync.Status) []string {
	var parts []string
	var size int64
	for _, c := range st.Changes {
		size += c.Size
	}
	switch {
	case len(st.Changes) > 0:
		parts = append(parts, count(len(st.Changes))+" to commit", human(size))
	case len(st.Conflicts) == 0 && st.Remote != nil && st.Remote.Ahead == 0:
		parts = append(parts, "up to date")
	case len(st.Conflicts) == 0:
		parts = append(parts, "nothing to commit")
	}
	if n := len(st.Conflicts); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "conflict", "conflicts")))
	}
	switch {
	case st.RemoteError != nil:
		parts = append(parts, "server not reachable: "+st.RemoteError.Error())
	case st.Remote != nil && st.Remote.Ahead > 0:
		parts = append(parts, fmt.Sprintf("server %d %s ahead", st.Remote.Ahead, plural(int(st.Remote.Ahead), "commit", "commits")))
	}
	if st.Remote != nil && len(st.Remote.Uploaded) > 0 {
		parts = append(parts, count(len(st.Remote.Uploaded))+" already uploaded")
	}
	if st.Remote != nil && len(st.Changes) > listLimit && len(st.Remote.Both) > 0 {
		parts = append(parts, count(len(st.Remote.Both))+" changed on both sides")
	}
	if !st.Pulled {
		parts = append(parts, "never pulled here")
	}
	if st.Synced > 0 {
		parts = append(parts, count(st.Synced)+" synced")
	}
	return parts
}

var changeMarks = map[sync.ChangeKind]string{sync.Added: "+", sync.Modified: "~", sync.Deleted: "-"}

func changeList(st sync.Status, s style) []string {
	both, uploaded := remoteSets(st)
	lines := make([]string, 0, len(st.Changes))
	for _, c := range st.Changes {
		detail := ""
		if c.Kind != sync.Deleted {
			detail = human(c.Size)
		}
		if uploaded[c.Path] {
			detail = strings.TrimPrefix(detail+" · uploaded, not committed", " · ")
		}
		if both[c.Path] {
			detail = strings.TrimPrefix(detail+" · also changed on the server", " · ")
		}
		line := "  " + changeMarks[c.Kind] + " " + c.Path
		if detail != "" {
			line += "  " + s.secondary(detail)
		}
		lines = append(lines, line)
	}
	return lines
}

// remoteSets: the changes also made on the server, and the ones whose
// bytes it already holds.
func remoteSets(st sync.Status) (both, uploaded map[string]bool) {
	both, uploaded = map[string]bool{}, map[string]bool{}
	if st.Remote == nil {
		return both, uploaded
	}
	for _, p := range st.Remote.Both {
		both[p] = true
	}
	for _, p := range st.Remote.Uploaded {
		uploaded[p] = true
	}
	return both, uploaded
}

type group struct {
	name     string
	files    int
	uploaded int
	bytes    int64
	items    []sync.LocalChange
}

// groupOf names the part of the store a path belongs to, in the words the
// user knows: chats, images and reasoning rather than directory names.
func groupOf(p string) (name, prefix string) {
	const conv = "conversations/"
	switch {
	case strings.HasPrefix(p, conv+"assets/"):
		return "images", conv + "assets/"
	case strings.HasPrefix(p, conv+"reasoning/"):
		return "reasoning", conv + "reasoning/"
	case strings.HasPrefix(p, conv+"imported/"):
		tool, _, _ := strings.Cut(strings.TrimPrefix(p, conv+"imported/"), "/")
		return "imported " + tool, conv + "imported/" + tool + "/"
	case strings.HasPrefix(p, conv):
		return "chats", conv
	}
	if dir, _, ok := strings.Cut(p, "/"); ok {
		return dir, dir + "/"
	}
	return "settings", ""
}

func groupChanges(changes []sync.LocalChange, uploaded map[string]bool) []group {
	byName := map[string]*group{}
	var groups []*group
	for _, c := range changes {
		name, _ := groupOf(c.Path)
		g, ok := byName[name]
		if !ok {
			g = &group{name: name}
			byName[name] = g
			groups = append(groups, g)
		}
		g.files++
		g.bytes += c.Size
		if uploaded[c.Path] {
			g.uploaded++
		}
		g.items = append(g.items, c)
	}
	out := make([]group, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b group) int {
		return cmp.Or(cmp.Compare(b.bytes, a.bytes), cmp.Compare(a.name, b.name))
	})
	if len(out) <= maxGroups {
		return out
	}
	rest := group{name: fmt.Sprintf("+%d smaller", len(out)-maxGroups+1)}
	for _, g := range out[maxGroups-1:] {
		rest.files += g.files
		rest.uploaded += g.uploaded
		rest.bytes += g.bytes
	}
	return append(out[:maxGroups-1], rest)
}

func changeGroups(st sync.Status, s style) []string {
	var total int64
	kinds := map[sync.ChangeKind]int{}
	for _, c := range st.Changes {
		total += c.Size
		kinds[c.Kind]++
	}
	var mix []string
	for _, k := range []struct {
		kind sync.ChangeKind
		word string
	}{{sync.Added, "new"}, {sync.Modified, "edited"}, {sync.Deleted, "deleted"}} {
		if kinds[k.kind] > 0 {
			mix = append(mix, count(kinds[k.kind])+" "+k.word)
		}
	}
	_, uploaded := remoteSets(st)
	groups := groupChanges(st.Changes, uploaded)
	header := []string{"group", "files", "uploaded", "size", "share", "largest"}
	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, []string{g.name, count(g.files), count(g.uploaded), human(g.bytes), share(g.bytes, total), ""})
	}
	widths := make([]int, len(header)-1)
	for _, r := range append([][]string{header}, rows...) {
		for i := range widths {
			widths[i] = max(widths[i], utf8.RuneCountInString(r[i]))
		}
	}
	// Two spaces and the box are the row's margin, and a space follows
	// every column.
	peekRoom := lineWidth - 4
	for _, w := range widths {
		peekRoom -= w + 1
	}
	lines := []string{
		"  " + s.secondary(strings.Join(mix, " · ")),
		"    " + s.secondary(columns(header, widths, peekRoom)),
	}
	for i, r := range rows {
		r[len(r)-1] = peek(groups[i], peekRoom)
		lines = append(lines, "  "+s.primary("■")+" "+columns(r, widths, peekRoom))
	}
	return lines
}

// columns lays out a row: the name padded, the counts right-aligned and
// the last cell, the peek, cut to what is left of the line.
func columns(cells []string, widths []int, peekRoom int) string {
	var line strings.Builder
	line.WriteString(pad(cells[0], widths[0]))
	for i := 1; i < len(widths); i++ {
		line.WriteString(" " + right(cells[i], widths[i]))
	}
	line.WriteString(" " + cut(cells[len(cells)-1], peekRoom))
	return strings.TrimRight(line.String(), " ")
}

// peek names the largest files of a group, as many as fit in room, and
// counts the rest.
func peek(g group, room int) string {
	if len(g.items) == 0 {
		return ""
	}
	items := slices.Clone(g.items)
	slices.SortFunc(items, func(a, b sync.LocalChange) int {
		return cmp.Or(cmp.Compare(b.Size, a.Size), cmp.Compare(a.Path, b.Path))
	})
	_, prefix := groupOf(items[0].Path)
	var parts []string
	for _, c := range items[:min(peekItems, len(items))] {
		parts = append(parts, cut(strings.TrimSuffix(strings.TrimPrefix(c.Path, prefix), ".json"), peekName)+" "+human(c.Size))
	}
	for {
		text := strings.Join(parts, " · ")
		if more := len(items) - len(parts); more > 0 {
			text = strings.TrimPrefix(text+" · +"+count(more)+" more", " · ")
		}
		if len(parts) <= 1 || utf8.RuneCountInString(text) <= room {
			return text
		}
		parts = parts[:len(parts)-1]
	}
}

func excludedLines(excluded []sync.Excluded, s style) []string {
	var lines []string
	secrets, imports := 0, 0
	var large []string
	for _, x := range excluded {
		switch x.Kind {
		case sync.ExcludedSecret:
			secrets++
			lines = append(lines, "  not synced, holds a secret: "+x.Path+"  "+s.secondary(x.Detail))
		case sync.ExcludedTooLarge:
			large = append(large, x.Path+" (over "+x.Detail+")")
		case sync.ExcludedImport:
			imports++
		}
	}
	if secrets > 0 {
		lines = append(lines, "    "+s.secondary("move the value to an env var or envfile, then commit"))
	}
	if len(large) > 0 {
		shown := large[:min(peekItems, len(large))]
		text := strings.Join(shown, " · ")
		if more := len(large) - len(shown); more > 0 {
			text += " · +" + count(more) + " more"
		}
		lines = append(lines, "  not synced, too large: "+s.secondary(text))
	}
	if imports > 0 {
		lines = append(lines, fmt.Sprintf("  %s imported %s from other tools left out; add them with commit -imports", count(imports), plural(imports, "file", "files")))
	}
	return lines
}

func blockedLines(store sync.Store, b *sync.BillingBlockedError, s style) []string {
	reason := "needs a card on file, $1 per started GB a month"
	switch b.Reason {
	case sync.PaymentOverdue:
		reason = "a payment is 7 days overdue"
	case sync.BillingNotOpen:
		return []string{s.head(string(store), "not committed", "conversation sync is not open yet; config sync works now")}
	}
	lines := []string{s.head(string(store), "not committed", reason)}
	if b.URL != "" {
		lines = append(lines, s.link("  ", b.URL))
	}
	return lines
}

// count prints a count below 1000 exactly and above it with one decimal
// and an SI prefix.
func count(n int) string {
	switch {
	case n >= 999_500:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func share(part, total int64) string {
	if total <= 0 {
		return "0%"
	}
	pct := (part*100 + total/2) / total
	if pct == 0 && part > 0 {
		return "<1%"
	}
	return fmt.Sprintf("%d%%", pct)
}

func pad(t string, width int) string {
	return t + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(t)))
}

func right(t string, width int) string {
	return strings.Repeat(" ", max(0, width-utf8.RuneCountInString(t))) + t
}

func cut(t string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(t)
	if len(runes) <= width {
		return t
	}
	return string(runes[:width-1]) + "…"
}
