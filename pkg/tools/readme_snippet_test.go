package tools

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/pkg/text/models"
)

// The declarations below are the custom-tool snippet from pkg/tools/README.md,
// verbatim. The test pins that the documented shape satisfies the LLMTool
// contract, so the README cannot drift from the interface.
type PingTool models.Specification

var Ping = PingTool{
	Name:        "ping",
	Description: "Reply with pong.",
}

func (p PingTool) Specification() models.Specification { return models.Specification(p) }

func (p PingTool) Call(models.Input) (string, error) { return "pong", nil }

func TestPkgToolsREADMESnippet(t *testing.T) {
	var tool models.LLMTool = Ping
	if got := tool.Specification().Name; got != "ping" {
		t.Fatalf("Specification().Name = %q, want ping", got)
	}
	out, err := tool.Call(models.Input{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "pong" {
		t.Fatalf("Call = %q, want pong", out)
	}
}

// TestREADMECatalogIsComplete pins the catalog table in pkg/tools/README.md
// against the exported tool values. A new exported tool must add its row; a
// renamed tool must update its row. The check runs both ways, so a row without
// a tool fails too.
func TestREADMECatalogIsComplete(t *testing.T) {
	documented, err := readCatalogRows("README.md")
	if err != nil {
		t.Fatalf("catalog rows: %v", err)
	}
	if len(documented) == 0 {
		t.Fatal("no catalog rows parsed from README.md; the table format changed")
	}
	for name := range documented {
		if _, exported := exportedToolNames()[name]; !exported {
			t.Errorf("README.md documents tool %q, which no exported tool reports", name)
		}
	}
	for name := range exportedToolNames() {
		if _, found := documented[name]; !found {
			t.Errorf("tool %q has no README.md row", name)
		}
	}
}

// readCatalogRows returns the tool names of a two-column markdown table whose
// first cell is a backticked name and second cell a backticked Go value.
func readCatalogRows(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]string)
	for line := range strings.SplitSeq(string(b), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(cells[1]), "`")
		value := strings.Trim(strings.TrimSpace(cells[2]), "`")
		if name == "" || value == "" || !strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			continue
		}
		rows[name] = value
	}
	return rows, nil
}

// exportedToolNames returns the name every exported tool value reports.
func exportedToolNames() map[string]struct{} {
	all := []models.LLMTool{
		ApplyPatch, AudioTranscribe, Cat, ClaiCheck, ClaiHelp, ClaiResult, ClaiRun,
		ClaiWaitForWorkers, Cmd, Cp, Date, FFProbe, FileTree, FileType, Find, Git,
		Go, Head, JQ, LineCount, LoadSkill, LS, Mkdir, Mktemp, Pwd, RipGrep,
		RowsBetween, Rsync, Sed, Tail, WebsiteText, WriteFile,
		AsyncCmdRun, AsyncCmdStatus, AsyncCmdLogs, AsyncCmdAwait, AsyncCmdCancel,
	}
	out := make(map[string]struct{}, len(all))
	for _, tool := range all {
		out[tool.Specification().Name] = struct{}{}
	}
	return out
}
