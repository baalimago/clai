package chat

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestSnippetFor_RuneSafeWithMultibyteContent guards that a keyword window landing
// inside multibyte runes (CJK/emoji, common in a real corpus) never slices a rune
// in half, which would emit invalid UTF-8 into the tool result returned to the model.
func TestSnippetFor_RuneSafeWithMultibyteContent(t *testing.T) {
	pad := strings.Repeat("日本語テスト🔧", 10)
	content := pad + " TARGET keyword here " + pad

	snippet := snippetFor(content, strings.ToLower(content), []string{"target"})
	if !utf8.ValidString(snippet) {
		t.Fatalf("snippet is not valid UTF-8: %q", snippet)
	}
	if !strings.Contains(snippet, "TARGET") {
		t.Fatalf("expected the keyword preserved in the snippet, got %q", snippet)
	}
}

// TestSnippetFor_OffsetSurvivesLengthChangingFold guards that a rune whose
// lowercasing changes byte width (U+212A KELVIN SIGN 'K' -> 'k', 3 bytes -> 1)
// before the keyword does not shift the snippet window off the keyword: the match
// offset is found in the lowercased copy but mapped back to a content byte offset.
func TestSnippetFor_OffsetSurvivesLengthChangingFold(t *testing.T) {
	// Several Kelvin signs (3 bytes each, fold to 1-byte 'k') ahead of the keyword
	// make the lowercased copy shorter than the original, so a naive lower-offset
	// applied to content would land before the keyword.
	prefix := strings.Repeat("K", 40) + " padding words to push the keyword past the radius window here we go "
	content := prefix + "DISTINCTKEYWORD trailing context"

	snippet := snippetFor(content, strings.ToLower(content), []string{"distinctkeyword"})
	if !utf8.ValidString(snippet) {
		t.Fatalf("snippet not valid UTF-8: %q", snippet)
	}
	if !strings.Contains(strings.ToLower(snippet), "distinctkeyword") {
		t.Fatalf("expected the keyword centred in the snippet, got %q", snippet)
	}
}

// TestSearch_PaginationOverflowDoesNotPanic guards the slice math against an int
// overflow when a hostile/huge page is supplied (page*pageSize can wrap negative).
func TestSearch_PaginationOverflowDoesNotPanic(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	dir := t.TempDir()
	seedChat(t, confDir, "h", dir, msg("user", "find me oauth here"))
	s := NewConversationSearcher(confDir)

	// page chosen so page*pageSize lands in [2^63, 2^64) -> negative int64.
	res, err := s.Search(SearchRequest{Query: "oauth", Directory: dir, Subtree: true, Page: 184467440737095517, PageSize: 50})
	if err != nil {
		t.Fatalf("Search overflow page: %v", err)
	}
	if res.TotalMatches != 1 || len(res.Rows) != 0 {
		t.Fatalf("expected total=1 with an out-of-range empty page, got total=%d rows=%d", res.TotalMatches, len(res.Rows))
	}

	out, err := InspectConversation(confDir, "h", 184467440737095517, 50, "", "")
	if err != nil {
		t.Fatalf("InspectConversation overflow page: %v", err)
	}
	if !strings.Contains(out, "Conversation h:") {
		t.Fatalf("expected a valid (empty) inspect page on overflow, got %q", out)
	}
}

// TestTokenizeQuery_QuoteAbuttingWord guards that an opening quote glued to a word
// splits into separate tokens instead of merging into one unmatchable token.
func TestTokenizeQuery_QuoteAbuttingWord(t *testing.T) {
	got := tokenizeQuery(`error"db connection"`)
	want := []string{"error", "db connection"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d: expected %q, got %q (all: %v)", i, want[i], got[i], got)
		}
	}
}

func seedChat(t *testing.T, confDir, id, originDir string, msgs ...pub_models.Message) {
	t.Helper()
	canon := originDir
	if originDir != "" {
		c, err := canonicalDir(originDir)
		if err != nil {
			t.Fatalf("canonicalDir(%q): %v", originDir, err)
		}
		canon = c
	}
	chat := pub_models.Chat{ID: id, OriginDir: canon, Messages: msgs}
	if err := Save(conversationsDir(confDir), chat); err != nil {
		t.Fatalf("Save(%q): %v", id, err)
	}
}

func msg(role, content string) pub_models.Message {
	return pub_models.Message{Role: role, Content: content}
}

func TestSearch_ANDSemanticsSubtreeAndExclusion(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	projRoot := t.TempDir()
	child := projRoot + "/svc"
	if err := utils.CreateConfigDir(child); err != nil { // just to mkdir
		t.Fatalf("mk child: %v", err)
	}

	// both tokens present, in the child dir
	seedChat(t, confDir, "hit", child,
		msg("system", "ignored system prompt"),
		msg("user", "fix the oauth refresh bug"),
		msg("assistant", "patched the refresh token flow"),
	)
	// missing one token (no "refresh")
	seedChat(t, confDir, "partial", child,
		msg("user", "fix the oauth login bug"),
	)
	// token only in leading system message -> must not match
	seedChat(t, confDir, "sysonly", child,
		msg("system", "phantomtoken in system"),
		msg("user", "unrelated content"),
	)

	s := NewConversationSearcher(confDir)

	// AND semantics, anchored at child (exact).
	res, err := s.Search(SearchRequest{Query: "oauth refresh", Directory: child, Subtree: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalMatches != 1 || res.Rows[0].ChatID != "hit" {
		t.Fatalf("expected only 'hit', got total=%d rows=%+v", res.TotalMatches, res.Rows)
	}

	// Subtree from the parent root finds the child conversation.
	res, _ = s.Search(SearchRequest{Query: "oauth refresh", Directory: projRoot, Subtree: true})
	if res.TotalMatches != 1 {
		t.Fatalf("expected subtree match from parent, got %d", res.TotalMatches)
	}
	// subtree=false from the parent excludes the descendant.
	res, _ = s.Search(SearchRequest{Query: "oauth refresh", Directory: projRoot, Subtree: false})
	if res.TotalMatches != 0 {
		t.Fatalf("expected no exact match from parent, got %d", res.TotalMatches)
	}

	// Anchored elsewhere returns none.
	res, _ = s.Search(SearchRequest{Query: "oauth", Directory: t.TempDir(), Subtree: true})
	if res.TotalMatches != 0 {
		t.Fatalf("expected no match in unrelated dir, got %d", res.TotalMatches)
	}

	// Leading-system-message token never matches.
	res, _ = s.Search(SearchRequest{Query: "phantomtoken", Directory: child, Subtree: true})
	if res.TotalMatches != 0 {
		t.Fatalf("expected system-only token to not match, got %d", res.TotalMatches)
	}
}

// TestSearch_MatchesHTMLEscapedTokens guards the stage-1 raw-byte prefilter against
// a false negative: Save persists conversations with encoding/json's default HTML
// escaping, so '<', '>' and '&' live on disk as < / > / &. A query
// token carrying one of those characters (an HTML tag, a generic type, "a&b") must
// still surface its conversation rather than being silently dropped before parse.
func TestSearch_MatchesHTMLEscapedTokens(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	dir := t.TempDir()
	seedChat(t, confDir, "htmlhit", dir,
		msg("user", "how do I render a <div> and escape a&b in this code"),
	)

	s := NewConversationSearcher(confDir)
	for _, q := range []string{"<div>", "a&b", `"<div>"`} {
		res, err := s.Search(SearchRequest{Query: q, Directory: dir, Subtree: true})
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if res.TotalMatches != 1 || res.Rows[0].ChatID != "htmlhit" {
			t.Fatalf("query %q: expected the html conversation, got total=%d rows=%+v", q, res.TotalMatches, res.Rows)
		}
	}
}

func TestSearch_PhraseRankingAndPagination(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	dir := t.TempDir()

	// "high" mentions the phrase several times -> higher score.
	seedChat(t, confDir, "high", dir,
		msg("user", "deploy pipeline deploy pipeline deploy pipeline"),
	)
	seedChat(t, confDir, "low", dir,
		msg("user", "deploy pipeline once"),
	)
	// Contains the words but not the contiguous phrase.
	seedChat(t, confDir, "nophrase", dir,
		msg("user", "pipeline that we deploy separately"),
	)

	s := NewConversationSearcher(confDir)

	// Quoted phrase: only contiguous "deploy pipeline" matches.
	res, _ := s.Search(SearchRequest{Query: `"deploy pipeline"`, Directory: dir, Subtree: true})
	if res.TotalMatches != 2 {
		t.Fatalf("expected 2 phrase matches, got %d rows=%+v", res.TotalMatches, res.Rows)
	}
	if res.Rows[0].ChatID != "high" {
		t.Fatalf("expected 'high' ranked first, got %q", res.Rows[0].ChatID)
	}
	if res.Rows[0].Snippet == "" {
		t.Fatalf("expected a non-empty snippet")
	}

	// Pagination: page size 1 reports full total but one row.
	res, _ = s.Search(SearchRequest{Query: `"deploy pipeline"`, Directory: dir, Subtree: true, PageSize: 1, Page: 0})
	if res.TotalMatches != 2 || len(res.Rows) != 1 {
		t.Fatalf("expected total 2 with 1 row, got total=%d rows=%d", res.TotalMatches, len(res.Rows))
	}
	res2, _ := s.Search(SearchRequest{Query: `"deploy pipeline"`, Directory: dir, Subtree: true, PageSize: 1, Page: 1})
	if len(res2.Rows) != 1 || res2.Rows[0].ChatID == res.Rows[0].ChatID {
		t.Fatalf("expected a distinct row on page 1, got %+v", res2.Rows)
	}
}

func TestInspectConversation_PaginationFiltersAndStorageTrueIndices(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	seedChat(t, confDir, "conv", t.TempDir(),
		msg("system", "the system prompt"),
		msg("user", "first question about widgets"),
		msg("assistant", "first answer"),
		msg("user", "second question about gadgets"),
		msg("assistant", "second answer"),
	)

	out, err := InspectConversation(confDir, "conv", 0, 20, "", "")
	if err != nil {
		t.Fatalf("InspectConversation: %v", err)
	}
	if !strings.Contains(out, "Conversation conv: 5 message(s)") {
		t.Fatalf("expected 5-message header, got %q", out)
	}
	if !strings.Contains(out, "index=0 role=system") {
		t.Fatalf("expected leading system message listed at index 0, got %q", out)
	}

	// role filter keeps storage-true indices (user is at 1 and 3).
	out, _ = InspectConversation(confDir, "conv", 0, 20, "user", "")
	if !strings.Contains(out, "index=1 role=user") || !strings.Contains(out, "index=3 role=user") {
		t.Fatalf("expected user indices 1 and 3, got %q", out)
	}
	if strings.Contains(out, "role=assistant") || strings.Contains(out, "role=system") {
		t.Fatalf("expected only user rows, got %q", out)
	}

	// match filter
	out, _ = InspectConversation(confDir, "conv", 0, 20, "", "gadgets")
	if !strings.Contains(out, "index=3 role=user") || strings.Contains(out, "index=1 role=user") {
		t.Fatalf("expected only the gadgets message (index 3), got %q", out)
	}
}

func TestReadMessage_ResolutionAndOutOfRange(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	seedChat(t, confDir, "conv", t.TempDir(),
		msg("system", "sys"),
		msg("user", "hello there"),
	)

	content, path, err := ReadMessage(confDir, "conv", 1)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !strings.Contains(content, "[user]") || !strings.Contains(content, "hello there") {
		t.Fatalf("expected role-tagged user content, got %q", content)
	}
	if path == "" {
		t.Fatalf("expected non-empty on-disk path")
	}

	if _, _, err := ReadMessage(confDir, "conv", 99); err == nil {
		t.Fatalf("expected out-of-range error")
	}
	if _, _, err := ReadMessage(confDir, "missing", 0); err == nil {
		t.Fatalf("expected unresolvable chat_id error")
	}
}

func TestBuildLookbackDescriptor_StatsAndCap(t *testing.T) {
	cq, confDir := newTestHandler(t)
	dir := t.TempDir()

	seedChat(t, confDir, "older", dir, msg("user", "older topic"), msg("assistant", "a"))
	seedChat(t, confDir, "newer", dir, msg("user", "newer topic"), msg("assistant", "b"), msg("user", "more"))

	if err := cq.SaveDirScope(dir, "older"); err != nil {
		t.Fatalf("SaveDirScope(older): %v", err)
	}
	if err := cq.SaveDirScope(dir, "newer"); err != nil {
		t.Fatalf("SaveDirScope(newer): %v", err)
	}

	// Full descriptor.
	desc, err := BuildLookbackDescriptor(confDir, dir, 5)
	if err != nil {
		t.Fatalf("BuildLookbackDescriptor: %v", err)
	}
	if !desc.HasHistory || desc.TotalChats != 2 || desc.Shown != 2 {
		t.Fatalf("expected 2 recorded/shown, got %+v", desc)
	}
	if desc.TotalMsgs != 5 { // 2 + 3
		t.Fatalf("expected aggregate 5 messages, got %d", desc.TotalMsgs)
	}
	if !strings.Contains(desc.Block, "This directory has 2 recorded conversation(s) (showing the 2 most recent, 5 message(s) total).") {
		t.Fatalf("unexpected header: %q", desc.Block)
	}
	if !strings.Contains(desc.Block, "<recent_conversations>") || !strings.Contains(desc.Block, `id="newer"`) {
		t.Fatalf("expected recent_conversations entries, got %q", desc.Block)
	}

	// Cap: injectCount 1 shows only the newest.
	desc, _ = BuildLookbackDescriptor(confDir, dir, 1)
	if desc.Shown != 1 || desc.TotalChats != 2 {
		t.Fatalf("expected shown=1 total=2, got %+v", desc)
	}
	if strings.Contains(desc.Block, `id="older"`) {
		t.Fatalf("expected capped block to omit older, got %q", desc.Block)
	}

	// No-history directory.
	desc, _ = BuildLookbackDescriptor(confDir, t.TempDir(), 5)
	if desc.HasHistory || desc.Block != "" {
		t.Fatalf("expected empty descriptor for dir without history, got %+v", desc)
	}
}
