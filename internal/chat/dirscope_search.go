package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// defaultSearchPageSize and defaultInspectPageSize are the fallback page sizes
// applied when a tool call omits page_size. maxSearchPageSize caps it so a single
// call can not request an unbounded page.
const (
	defaultSearchPageSize  = 10
	defaultInspectPageSize = 20
	maxSearchPageSize      = 50
	maxInspectPageSize     = 200
	snippetRadius          = 80
)

// ConversationSearcher finds conversations anchored to a directory. It is an
// interface so an inverted-index implementation can replace the brute-force scan
// without changing callers once a corpus outgrows linear scanning (see
// architecture/dirscope.md for the documented upgrade trigger).
type ConversationSearcher interface {
	Search(req SearchRequest) (SearchResult, error)
}

// SearchRequest anchors a keyword search to a directory.
//
// Subtree has no defaulting here: the zero value is false (exact origin_dir match).
// The documented "default true" lives at the tool boundary (runLookbackTool reads
// subtree with a true fallback); direct callers of Search must set it explicitly.
type SearchRequest struct {
	Query     string
	Directory string // canonical path anchoring the search
	Subtree   bool   // true: match Directory and anything nested beneath it; false: exact match
	Page      int
	PageSize  int
}

// SearchResultRow is a single ranked match.
type SearchResultRow struct {
	ChatID   string
	Created  time.Time
	Model    string
	MsgCount int
	ByteSize int
	Score    int
	Snippet  string
}

// SearchResult is a page of ranked matches plus the total breadth.
type SearchResult struct {
	Directory    string
	TotalMatches int
	Page         int
	PageSize     int
	Rows         []SearchResultRow
}

type bruteForceSearcher struct {
	confDir string
}

// NewConversationSearcher returns the brute-force, dir-anchored searcher.
func NewConversationSearcher(confDir string) ConversationSearcher {
	return &bruteForceSearcher{confDir: confDir}
}

// Search executes the dir-anchored keyword pipeline: metadata prefilter on the
// chat index, raw-byte content prefilter, then parse-and-rank survivors with a
// keyword-centred snippet, paginated.
func (s *bruteForceSearcher) Search(req SearchRequest) (SearchResult, error) {
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = defaultSearchPageSize
	}
	if pageSize > maxSearchPageSize {
		pageSize = maxSearchPageSize
	}
	page := max(req.Page, 0)

	dir, err := canonicalDir(req.Directory)
	if err != nil {
		return SearchResult{}, fmt.Errorf("canonicalize search directory %q: %w", req.Directory, err)
	}
	result := SearchResult{Directory: dir, Page: page, PageSize: pageSize}

	tokens := tokenizeQuery(req.Query)

	convDir := conversationsDir(s.confDir)
	rows, err := readChatIndex(convDir)
	if err != nil {
		return SearchResult{}, fmt.Errorf("read chat index: %w", err)
	}

	// Candidates are restricted to the queried directory subtree, which is a small
	// slice of the corpus, so a sequential scan of survivors is sufficient; the
	// ConversationSearcher interface lets an index-backed scan replace this if a
	// corpus ever outgrows linear scanning (see architecture/dirscope.md).
	matches := make([]SearchResultRow, 0)
	for _, row := range rows {
		// globalScope is a transient pointer to the most recent conversation; it
		// would double-count the real conversation it mirrors.
		if row.ID == globalScopeChatID {
			continue
		}
		if !originMatches(row.OriginDir, dir, req.Subtree) {
			continue
		}
		raw, err := os.ReadFile(conversationPath(s.confDir, row.ID))
		if err != nil {
			continue // a vanished/locked file is simply not a match
		}
		// Stage 1: cheap raw-byte AND prefilter (may over-include system-msg-only hits).
		if !rawContainsAll(raw, tokens) {
			continue
		}
		// Stage 2: parse (reusing the bytes already read), build matchable content
		// excluding the leading system message, re-check AND semantics (drops phantom
		// system-message-only hits), then rank.
		var chat pub_models.Chat
		if err := json.Unmarshal(raw, &chat); err != nil {
			continue
		}
		content, firstUser := searchableContent(chat)
		lowerContent := strings.ToLower(content)
		if !containsAllTokens(lowerContent, tokens) {
			continue
		}
		matches = append(matches, SearchResultRow{
			ChatID:   row.ID,
			Created:  row.Created,
			Model:    row.Model,
			MsgCount: row.MessageCount,
			ByteSize: len(raw),
			Score:    scoreContent(lowerContent, strings.ToLower(firstUser), tokens),
			Snippet:  snippetFor(content, lowerContent, tokens),
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Created.After(matches[j].Created)
	})

	result.TotalMatches = len(matches)
	start := page * pageSize
	// start < 0 guards an int overflow from a hostile/huge page value (page*pageSize
	// can wrap negative), which would otherwise panic on the slice below.
	if start < 0 || start >= len(matches) {
		return result, nil
	}
	end := start + pageSize
	if end < start || end > len(matches) {
		end = len(matches)
	}
	result.Rows = matches[start:end]
	return result, nil
}

// tokenizeQuery splits a query into lowercased AND tokens. "Quoted phrases" are
// kept as a single contiguous-substring token; bare whitespace separates tokens.
func tokenizeQuery(query string) []string {
	tokens := make([]string, 0)
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range query {
		switch {
		case r == '"':
			// Flush any partial token at either quote boundary, so an opening quote
			// glued to a word (e.g. error"db connection") splits into ["error",
			// "db connection"] rather than merging into one unmatchable token.
			flush()
			inQuote = !inQuote
		case (r == ' ' || r == '\t' || r == '\n') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// searchableContent returns the lowercase-safe matchable content (all messages
// except the leading system message) and the first user message separately for
// scoring weight.
func searchableContent(chat pub_models.Chat) (content, firstUser string) {
	var sb strings.Builder
	for i, msg := range chat.Messages {
		if i == 0 && msg.Role == "system" {
			continue // skip the configured system prompt / injected descriptor blocks
		}
		body := msg.String()
		if body == "" {
			continue
		}
		if firstUser == "" && msg.Role == "user" {
			firstUser = body
		}
		sb.WriteString(body)
		sb.WriteByte('\n')
	}
	return sb.String(), firstUser
}

func rawContainsAll(raw []byte, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	lower := bytes.ToLower(raw)
	for _, tok := range tokens {
		if !rawContainsToken(lower, tok) {
			return false
		}
	}
	return true
}

// rawContainsToken reports whether the lowercased raw JSON bytes contain tok.
// Conversation files are persisted with encoding/json's default HTML escaping, so
// '<', '>' and '&' are stored as their < / > / & escapes. A token
// carrying one of those characters would never match the raw bytes literally even
// though the parsed (stage-2) content contains it, so the prefilter must also try
// the escaped rendering — otherwise a real match for e.g. "<div>" or "a&b" is
// silently dropped before it is ever parsed.
func rawContainsToken(lowerRaw []byte, tok string) bool {
	if bytes.Contains(lowerRaw, []byte(tok)) {
		return true
	}
	if !strings.ContainsAny(tok, "<>&") {
		return false
	}
	return bytes.Contains(lowerRaw, []byte(jsonHTMLEscape(tok)))
}

// jsonHTMLEscape mirrors the three substitutions encoding/json applies to string
// bytes when HTML escaping is on (the default used by Save). The hex is lowercase
// to match bytes.ToLower'd raw content and the already-lowercased query token.
func jsonHTMLEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '<', '>', '&':
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func containsAllTokens(lowerContent string, tokens []string) bool {
	for _, tok := range tokens {
		if !strings.Contains(lowerContent, tok) {
			return false
		}
	}
	return true
}

// scoreContent is a transparent hit count: summed token occurrences across all
// searchable content, plus an extra 2x bonus for hits in the first user message
// (which is itself part of content), so a topic stated up front ranks higher.
func scoreContent(lowerContent, lowerFirstUser string, tokens []string) int {
	score := 0
	for _, tok := range tokens {
		score += strings.Count(lowerContent, tok)
		score += 2 * strings.Count(lowerFirstUser, tok)
	}
	return score
}

// snippetFor extracts a keyword-centred window from the original (cased) content
// so the agent judges fit from real context rather than a bare rank. lowerContent
// is the already-lowercased content (same rune sequence) — reused here rather than
// re-lowering the whole transcript. The match offset is found in lowerContent and
// mapped back to a byte offset in content via rune count, so a rune whose
// lowercasing changes byte width does not shift the window off the keyword.
func snippetFor(content, lowerContent string, tokens []string) string {
	idx := -1
	for _, tok := range tokens {
		if at := strings.Index(lowerContent, tok); at >= 0 && (idx == -1 || at < idx) {
			idx = at
		}
	}
	if idx <= 0 {
		idx = 0
	} else {
		idx = byteOffsetForRuneCount(content, utf8.RuneCountInString(lowerContent[:idx]))
	}
	start := max(idx-snippetRadius, 0)
	end := min(idx+snippetRadius, len(content))
	// idx is a byte offset and the window edges land at arbitrary bytes; snap both
	// to UTF-8 rune boundaries so a multibyte rune is never sliced in half (which
	// would emit invalid UTF-8 into the tool result fed back to the model).
	start = snapToRuneStart(content, start)
	end = snapToRuneStart(content, end)
	snippet := strings.TrimSpace(collapseWhitespace(content[start:end]))
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(content) {
		snippet += "…"
	}
	return snippet
}

// snapToRuneStart returns the largest index <= i that begins a UTF-8 rune in s
// (clamped to [0, len(s)]). Snapping both window edges back to a rune start keeps
// the sliced snippet valid UTF-8 even when the radius lands inside a multibyte rune.
func snapToRuneStart(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// byteOffsetForRuneCount returns the byte offset in s at the start of the n-th
// rune (clamped to len(s)). Used to translate a rune position found in a lowercased
// copy back into the original string whose byte widths may differ.
func byteOffsetForRuneCount(s string, n int) int {
	off := 0
	for i := 0; i < n && off < len(s); i++ {
		_, size := utf8.DecodeRuneInString(s[off:])
		off += size
	}
	return off
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// FormatSearchResult renders a SearchResult as the non-interactive tool output.
func FormatSearchResult(res SearchResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d match(es) in %s (page %d, showing %d):\n",
		res.TotalMatches, res.Directory, res.Page, len(res.Rows))
	for _, row := range res.Rows {
		fmt.Fprintf(&sb, "id=%s created=%s model=%s msgs=%d bytes=%d score=%d: %s\n",
			row.ChatID, humanizeAge(row.Created), modelOrUnknown(row.Model),
			row.MsgCount, row.ByteSize, row.Score, row.Snippet)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func modelOrUnknown(model string) string {
	if strings.TrimSpace(model) == "" {
		return "unknown"
	}
	return model
}

// InspectConversation returns a paginated per-message metadata listing (never
// message bodies) with stable storage-true indices and optional role/substring
// filters. The leading system prompt is listed (not hidden) to keep indices
// honest; its preview makes it obvious to skip.
func InspectConversation(confDir, chatID string, page, pageSize int, role, match string) (string, error) {
	if pageSize <= 0 {
		pageSize = defaultInspectPageSize
	}
	if pageSize > maxInspectPageSize {
		pageSize = maxInspectPageSize
	}
	if page < 0 {
		page = 0
	}
	chat, err := FromPath(conversationPath(confDir, chatID))
	if err != nil {
		return "", fmt.Errorf("load conversation %q: %w", chatID, err)
	}

	lowerRole := strings.ToLower(strings.TrimSpace(role))
	lowerMatch := strings.ToLower(strings.TrimSpace(match))
	type inspectRow struct {
		index   int
		role    string
		length  int
		preview string
	}
	rows := make([]inspectRow, 0, len(chat.Messages))
	for i, msg := range chat.Messages {
		body := msg.String()
		if lowerRole != "" && strings.ToLower(msg.Role) != lowerRole {
			continue
		}
		if lowerMatch != "" && !strings.Contains(strings.ToLower(body), lowerMatch) {
			continue
		}
		rows = append(rows, inspectRow{
			index:   i, // storage-true: never offset by hidden rows
			role:    msg.Role,
			length:  utf8.RuneCountInString(body),
			preview: previewOf(body, 80),
		})
	}

	total := len(rows)
	start := page * pageSize
	// start < 0 guards an int overflow from a hostile/huge page value.
	if start < 0 || start > total {
		start = total
	}
	end := start + pageSize
	if end < start || end > total {
		end = total
	}
	pageRows := rows[start:end]

	var sb strings.Builder
	fmt.Fprintf(&sb, "Conversation %s: %d message(s) (page %d, showing %d", chatID, total, page, len(pageRows))
	if lowerRole != "" {
		fmt.Fprintf(&sb, ", role=%s", lowerRole)
	}
	if lowerMatch != "" {
		fmt.Fprintf(&sb, ", match=%q", strings.TrimSpace(match))
	}
	sb.WriteString("):\n")
	for _, r := range pageRows {
		fmt.Fprintf(&sb, "index=%d role=%s length=%d: %s\n", r.index, r.role, r.length, r.preview)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ReadMessage returns the role-tagged content of a single message by its
// storage-true index, plus the on-disk conversation path so a caller can surface
// it when the content is truncated by the tool-output limit. An out-of-range
// index or unresolvable chat id returns an error.
func ReadMessage(confDir, chatID string, messageIndex int) (content, path string, err error) {
	path = conversationPath(confDir, chatID)
	chat, err := FromPath(path)
	if err != nil {
		return "", path, fmt.Errorf("load conversation %q: %w", chatID, err)
	}
	if messageIndex < 0 || messageIndex >= len(chat.Messages) {
		return "", path, fmt.Errorf("message_index %d out of range: conversation %q has %d message(s)", messageIndex, chatID, len(chat.Messages))
	}
	msg := chat.Messages[messageIndex]
	return fmt.Sprintf("[%s]\n%s", msg.Role, msg.String()), path, nil
}

func previewOf(body string, n int) string {
	body = collapseWhitespace(body)
	if utf8.RuneCountInString(body) <= n {
		return body
	}
	runes := []rune(body)
	return string(runes[:n]) + "…"
}

// humanizeAge renders a coarse relative age (e.g. "2d", "5h", "just now").
func humanizeAge(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
