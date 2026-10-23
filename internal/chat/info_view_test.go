package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vttest"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// editFlowChatJSON is shaped like a real conversation with a generate_image call.
const editFlowChatJSON = `{
  "created": "2026-10-23T14:23:29.865951621+03:00",
  "id": "globalScope",
  "profile": "Wasmer",
  "group_key": "ce94fa838541d5dc60e709c62eb281f103f4faf750f2da6fb10f8174c36caa61",
  "messages": [
    {"role": "system", "content": "<shell context>\nYou are a shell agent working in a tool called clai. clai is the agentic harness.\n%s\n<recent_conversations>\n<conversation messages=\"3\">Generate an image of a nice fluffy tasty cloud that I could eat for dinner</conversation>\n</recent_conversations>"},
    {"role": "user", "content": "Generate an image of five breakdancing cats and dogs please"},
    {"role": "assistant", "content": "", "tool_calls": [{"id": "call_m1XpPYC4AmOYywBELzA9onQ2", "name": "generate_image", "type": "function",
      "inputs": {"input_images": [], "prompt": "A joyful, dynamic scene featuring exactly five animals breakdancing together: three cats and two dogs. Richly detailed whimsical 3D animated illustration. No text.", "rebus": "🐱 🐶 🕺", "size": "1536x1024"},
      "function": {"name": "generate_image", "arguments": "{\"input_images\":[],\"prompt\":\"A joyful, dynamic scene featuring exactly five animals breakdancing together: three cats and two dogs. Richly detailed whimsical 3D animated illustration. No text.\",\"rebus\":\"🐱 🐶 🕺\",\"size\":\"1536x1024\"}"}}]},
    {"role": "tool", "tool_call_id": "call_m1XpPYC4AmOYywBELzA9onQ2", "content": [
      {"type": "text", "text": "generated image afbdcf saved at /home/x/.config/.clai/conversations/assets/afbdcf4443c60855c9ed5dd33f09d39918019cdc78e2a285b9fe9cb1fd397727.png"},
      {"type": "image", "asset": "afbdcf4443c60855c9ed5dd33f09d39918019cdc78e2a285b9fe9cb1fd397727.png", "mime": "image/png", "origin": "generated"}]}
  ]
}`

func editFlowChat(t *testing.T) pub_models.Chat {
	t.Helper()
	filler := strings.Repeat("You are helpful and terse. ", 1000)
	fillerJSON, _ := json.Marshal(filler)
	raw := fmt.Sprintf(editFlowChatJSON, strings.Trim(string(fillerJSON), `"`))
	var c pub_models.Chat
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return c
}

func editFlowConf(t *testing.T) (confDir, convDir string) {
	t.Helper()
	confDir = t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("NO_COLOR", "true")
	return confDir, conversationsDir(confDir)
}

type infoViewShape struct {
	name    string
	chat    pub_models.Chat
	foreign bool
}

func infoViewShapes(t *testing.T) []infoViewShape {
	t.Helper()
	gs := editFlowChat(t)
	noKey := gs
	noKey.GroupKey = ""
	labelled := gs
	labelled.Title, labelled.Summary = "Breakdancing pets", "Generated an image of five dancing cats and dogs."
	dirtyLabel := labelled
	dirtyLabel.Title, dirtyLabel.Summary = "Pets\x1b[1A\x1b[2K\rdancing", "Five\x1b[1A\x1b[2K\rcats"
	foreign := gs
	foreign.Source, foreign.SourceID, foreign.ID = "claude-code", "abc", ""
	foreignNoKey := foreign
	foreignNoKey.GroupKey = ""
	withFirst := func(text string) pub_models.Chat {
		c := gs
		c.Messages = slices.Clone(gs.Messages)
		c.Messages[1] = pub_models.Message{Role: "user", Content: text}
		return c
	}
	return []infoViewShape{
		{"native, escapes in the first message", withFirst("npm install failed\x1b[1A\x1b[2Kfetching\r50%"), false},
		{"native, CRLF between wide runes", withFirst(strings.Repeat("界", 20) + "\r\n" + strings.Repeat("界", 30)), false},
		{"native globalScope (group_key, no title)", gs, false},
		{"native, no group_key", noKey, false},
		{"native, group_key+title+summary", labelled, false},
		{"native, escapes in title and summary", dirtyLabel, false},
		{"foreign, group_key", foreign, true},
		{"foreign, no group_key", foreignNoKey, true},
	}
}

// answeringTerminal answers each prompt that ends in "): " from answers, by writing the
// answer to the TTY file the handler reads and echoing it, as a terminal would.
type answeringTerminal struct {
	w       io.Writer
	tty     string
	answers []string
	n       int
	snapAt  int
}

func (h *answeringTerminal) Write(p []byte) (int, error) {
	n, err := h.w.Write(p)
	if err != nil || !strings.HasSuffix(stripTermSGR(string(p)), "): ") {
		return n, err
	}
	if h.n == h.snapAt {
		io.WriteString(h.w, vttest.SnapshotCode())
	}
	a := "q"
	if h.n < len(h.answers) {
		a = h.answers[h.n]
	}
	h.n++
	if err := os.WriteFile(h.tty, []byte(a+"\n"), 0o600); err != nil {
		return n, err
	}
	io.WriteString(h.w, a+"\n") // the terminal's echo of the typed answer
	return n, nil
}

// runChatInfo draws the info view of chat on a width-wide terminal below a prompt and
// the typed command, answers b, and returns the screen and the rows the cursor ended
// above (negative) or below the view's first row.
func runChatInfo(t *testing.T, sh infoViewShape, w int) (int, []string) {
	t.Helper()
	work := t.TempDir()
	tty := filepath.Join(work, "tty")
	t.Setenv("TTY", tty)
	vt := &vttest.Terminal{Width: w}
	fmt.Fprint(vt, "PROMPT $\nclai chat list\n")
	hook := &answeringTerminal{w: vt, tty: tty, answers: []string{"b"}, snapAt: -1}
	cq := &ChatHandler{out: hook, dims: dimensions.Dimensions{Width: w}}
	var err error
	if sh.foreign {
		err = cq.actOnForeignChat(sh.chat, nil, "")
	} else {
		err = cq.actOnChat(sh.chat, "")
	}
	if err != nil {
		t.Fatalf("%s @%d: %v", sh.name, w, err)
	}
	if odd := vt.Unhandled(); len(odd) > 0 {
		t.Fatalf("%s @%d: sequences the test terminal does not model: %q", sh.name, w, odd)
	}
	return vt.Row() - 2, vt.Screen()
}

// The info view clears exactly the rows it drew, so backing out leaves the prompt and
// the typed command alone on every terminal width.
func TestChatInfoView_BackClearsWhatItDrew(t *testing.T) {
	editFlowConf(t)
	for _, sh := range infoViewShapes(t) {
		for _, w := range []int{40, 60, 63, 80, 120, 124, 125, 204} {
			offset, screen := runChatInfo(t, sh, w)
			if offset != 0 || len(screen) < 2 || screen[1] != "clai chat list" {
				t.Errorf("%s @%d: cursor offset %+d, screen %q; want 0 and the command kept", sh.name, w, offset, screen)
			}
		}
	}
}

// editFlow drives actOnChat on a 204-column terminal with answers, through an editor
// that logs each buffer and appends EDIT_APPEND to it, and returns the screen at the
// last prompt, the editor log and what the handler printed through ancli.
func editFlow(t *testing.T, appendText string, answers ...string) (screen []string, editorLog, notices string, convDir string) {
	t.Helper()
	_, convDir = editFlowConf(t)
	work := t.TempDir()
	if err := Save(convDir, editFlowChat(t)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	logPath := filepath.Join(work, "editor.log")
	editor := filepath.Join(work, "editor.sh")
	script := "#!/bin/sh\n{ printf '<<<'; cat \"$1\"; printf '>>>\\n'; } >> \"$EDITOR_LOG\"\n[ -n \"$EDIT_APPEND\" ] && printf '%s' \"$EDIT_APPEND\" >> \"$1\"\n[ -n \"$EDIT_CRLF\" ] && awk '{printf \"%s\\r\\n\", $0}' \"$1\" > \"$1.crlf\" && mv \"$1.crlf\" \"$1\"\nexit 0\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", editor)
	t.Setenv("EDITOR_LOG", logPath)
	t.Setenv("EDIT_APPEND", appendText)
	tty := filepath.Join(work, "tty")
	t.Setenv("TTY", tty)

	oldNL, oldColor, oldStdout := ancli.Newline, ancli.UseColor, os.Stdout
	ancli.Newline, ancli.UseColor = true, false
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = pw
	defer func() { ancli.Newline, ancli.UseColor, os.Stdout = oldNL, oldColor, oldStdout }()
	vt := &vttest.Terminal{Width: 204}
	fmt.Fprint(vt, "PROMPT $\nclai chat list\n")
	var printed strings.Builder
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.MultiWriter(vt, &printed), pr); done <- err }()

	hook := &answeringTerminal{w: pw, tty: tty, answers: answers, snapAt: len(answers) - 1}
	cq := &ChatHandler{convDir: convDir, out: hook, dims: dimensions.Dimensions{Width: 204}}
	chat, err := cq.getByID("globalScope")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	actErr := cq.actOnChat(chat, "")
	os.Stdout = oldStdout
	pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("copy: %v", err)
	}
	if actErr != nil {
		t.Fatalf("actOnChat: %v", actErr)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(vt.Snapshots()) == 0 {
		t.Fatal("no snapshot at the last prompt")
	}
	if odd := vt.Unhandled(); len(odd) > 0 {
		t.Fatalf("sequences the test terminal does not model: %q", odd)
	}
	return vt.Snapshots()[len(vt.Snapshots())-1], string(logged), printed.String(), convDir
}

// Opening messages without changing them saves nothing, also when the editor ends the
// buffer with a newline or rewrites it with CRLF line ends; a tool-call turn opens
// with its call as the preview shows it, and the screen keeps the typed command.
func TestChatEditFlow_InspectSavesNothing(t *testing.T) {
	for _, tc := range []struct {
		name, appendText, crlf string
	}{{"as shown", "", ""}, {"newline", "\n", ""}, {"crlf", "\n", "1"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EDIT_CRLF", tc.crlf)
			before := editFlowChat(t)
			screen, logged, printed, convDir := editFlow(t, tc.appendText, "e", "3", "2", "1", "0", "b")
			if len(screen) < 2 || screen[1] != "clai chat list" {
				t.Fatalf("the typed command was erased:\n%s", strings.Join(screen, "\n"))
			}
			if !strings.Contains(logged, "<<<Call: 'generate_image'") {
				t.Fatalf("editor buffers %q: want the tool-call turn to open with its call", logged)
			}
			if strings.Contains(printed, "modified chat") || strings.Contains(printed, "holds tool calls") {
				t.Fatalf("an unchanged edit was handled as a change: %q", printed)
			}
			after, err := FromPath(conversationPathFromDir(convDir, "globalScope"))
			if err != nil {
				t.Fatal(err)
			}
			b0, _ := json.Marshal(before.Messages)
			b1, _ := json.Marshal(after.Messages)
			if string(b0) != string(b1) {
				t.Fatalf("messages changed by inspecting them:\n before %.300s\n after  %.300s", b0, b1)
			}
		})
	}
}

// Leaving the delete picker without a choice keeps the typed command on screen and
// the conversation as it was.
func TestChatDeleteFlow_BackKeepsScreen(t *testing.T) {
	before := editFlowChat(t)
	screen, _, _, convDir := editFlow(t, "", "d", "b")
	if len(screen) < 2 || screen[1] != "clai chat list" {
		t.Fatalf("the typed command was erased:\n%s", strings.Join(screen, "\n"))
	}
	after, err := FromPath(conversationPathFromDir(convDir, "globalScope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Messages) != len(before.Messages) {
		t.Fatalf("%d messages after leaving the delete picker, want %d", len(after.Messages), len(before.Messages))
	}
}

// Changing a tool-call turn's text is refused, and the turn stays as it was; changing a
// text message saves it without the newline the editor added.
func TestChatEditFlow_EditsTextNotCalls(t *testing.T) {
	_, _, printed, convDir := editFlow(t, " (edited)\n", "e", "2", "1", "b")
	after, err := FromPath(conversationPathFromDir(convDir, "globalScope"))
	if err != nil {
		t.Fatal(err)
	}
	m1, m2 := after.Messages[1], after.Messages[2]
	if m2.Content != "" || len(m2.ToolCalls) != 1 || m2.ToolCalls[0].Name != "generate_image" {
		t.Fatalf("tool-call turn = %+v, want it unchanged", m2)
	}
	if !strings.HasSuffix(m1.Content, " (edited)") || !strings.Contains(printed, "message with index: '1'") {
		t.Fatalf("text message = %q, printed %q; want the edit saved", m1.Content, printed)
	}
}

var termSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripTermSGR(s string) string { return termSGR.ReplaceAllString(s, "") }

func TestApplyEdit(t *testing.T) {
	img := pub_models.ImagePart{Asset: "a.png", MIME: "image/png", Origin: pub_models.OriginUser}
	calls := []pub_models.Call{{ID: "c1", Name: "ls"}}
	text := func(s string) pub_models.Message { return pub_models.Message{Role: "user", Content: s} }
	for _, tc := range []struct {
		name    string
		m       pub_models.Message
		edited  string
		want    editOutcome
		content string
		parts   int
	}{
		{"editor newline only", text("abc"), "abc\n", editUnchanged, "abc", 0},
		{"crlf only", text("a\nb"), "a\r\nb\r\n", editUnchanged, "a\nb", 0},
		{"keeps a shown final newline", text("a\n"), "b\n", editSaved, "b\n", 0},
		{"drops the editor's newline", text("a"), "b\r\nc\r\n", editSaved, "b\nc", 0},
		{"blank text message", text("a"), " \n", editRefusedEmpty, "a", 0},
		{"blank text-only parts", pub_models.Message{Role: "user", Parts: []pub_models.Part{pub_models.TextPart{Text: "a"}}}, "", editRefusedEmpty, "", 1},
		{"blank keeps images", pub_models.Message{Role: "user", Parts: []pub_models.Part{img, pub_models.TextPart{Text: "a"}}}, "", editSaved, "", 1},
		{"calls only", pub_models.Message{Role: "assistant", ToolCalls: calls}, "x", editRefusedCalls, "", 0},
		{"blank prose beside calls opens the calls", pub_models.Message{Role: "assistant", Content: "\n", ToolCalls: calls}, "x", editRefusedCalls, "\n", 0},
		{"editor trims trailing spaces", text("a  \nb\t"), "a\nb\n", editUnchanged, "a  \nb\t", 0},
		{"blank prose beside calls", pub_models.Message{Role: "assistant", Content: "hi", ToolCalls: calls}, "", editSaved, "", 0},
	} {
		got, outcome := applyEdit(tc.m, tc.edited)
		if outcome != tc.want || got.Content != tc.content || len(got.Parts) != tc.parts {
			t.Errorf("%s: outcome %d, content %q, %d parts; want %d, %q, %d", tc.name, outcome, got.Content, len(got.Parts), tc.want, tc.content, tc.parts)
		}
	}
}

func TestMessagePickerRow_oneCleanRow(t *testing.T) {
	m := pub_models.Message{Role: "tool", Content: "\x1b[32madded 12 packages\x1b[0m\rprogress 50%\x1b[1A\x1b[2K\ndone"}
	row, err := messagePickerRow(3, m, 200)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(row, "\r\n\x1b") || !strings.Contains(row, "added 12 packages progress 50% done") {
		t.Fatalf("picker row %q: want the message on one clean row", row)
	}
}

func TestEmptyEditHint(t *testing.T) {
	if hint := emptyEditHint("tool"); strings.Contains(hint, "[d]elete") || !strings.Contains(hint, "replace") {
		t.Fatalf("tool hint %q: deleting a tool result orphans its call; want a hint to replace the text", hint)
	}
	if hint := emptyEditHint("user"); !strings.Contains(hint, "[d]elete") {
		t.Fatalf("user hint %q: want the delete action", hint)
	}
}

func TestEditorShown_blankProseShowsCalls(t *testing.T) {
	m := pub_models.Message{Role: "assistant", Content: "\n", ToolCalls: []pub_models.Call{{ID: "c1", Name: "ls"}}}
	if shown := editorShown(m); !strings.Contains(shown, "Call: 'ls'") {
		t.Fatalf("editor opens with %q, want the calls", shown)
	}
}
