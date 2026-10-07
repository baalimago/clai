package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestApplyPatchCallRejectsBlankPatch(t *testing.T) {
	if _, err := (ApplyPatchTool{}).Call(pub_models.Input{"patch": "   \n"}); err == nil {
		t.Fatal("expected an error for a whitespace-only patch")
	}
}

func TestParseApplyPatchErrors(t *testing.T) {
	testCases := []struct {
		name  string
		patch string
	}{
		{name: "scanner failure", patch: strings.Repeat("a", 70000)},
		{name: "empty", patch: ""},
		{name: "missing begin marker", patch: "*** Add File: x\n+one\n*** End Patch"},
		{name: "add file without a path", patch: "*** Begin Patch\n*** Add File: \n+one\n*** End Patch"},
		{name: "add file without content", patch: "*** Begin Patch\n*** Add File: x\n*** End Patch"},
		{name: "delete file without a path", patch: "*** Begin Patch\n*** Delete File: \n*** End Patch"},
		{name: "update file without a path", patch: "*** Begin Patch\n*** Update File: \n*** End Patch"},
		{
			name:  "move to without a path",
			patch: "*** Begin Patch\n*** Update File: x\n*** Move to: \n@@\n-a\n+b\n*** End Patch",
		},
		{name: "update file without a diff", patch: "*** Begin Patch\n*** Update File: x\n*** End Patch"},
		{name: "unexpected directive", patch: "*** Begin Patch\nsurprise\n*** End Patch"},
		{name: "missing end marker", patch: "*** Begin Patch\n*** Add File: x\n+one"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseApplyPatch(tc.patch); err == nil {
				t.Fatalf("parseApplyPatch accepted %s", tc.name)
			}
		})
	}
}

func TestSplitDiffIntoHunksErrors(t *testing.T) {
	testCases := []struct {
		name  string
		lines []string
	}{
		{name: "blank line", lines: []string{" context", ""}},
		{name: "unknown prefix", lines: []string{"!oops"}},
		{name: "header without an old range", lines: []string{"@@ nope"}},
		{name: "header between empty hunks", lines: []string{"@@", "@@"}},
		{name: "no hunks at all", lines: []string{"*** End of File"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := splitDiffIntoHunks(tc.lines); err == nil {
				t.Fatalf("splitDiffIntoHunks accepted %s", tc.name)
			}
		})
	}
}

func TestApplyDiffErrors(t *testing.T) {
	if _, err := applyDiff("alpha\n", nil, false); err == nil {
		t.Error("applyDiff accepted an empty diff")
	}
	if _, err := applyDiff("alpha\nbeta\n", []string{" gamma"}, false); err == nil {
		t.Error("applyDiff accepted a hunk that matches nowhere")
	}
}

func TestApplyHunkAtErrors(t *testing.T) {
	testCases := []struct {
		name  string
		lines []string
		hunk  diffHunk
	}{
		{name: "context beyond end of file", lines: nil, hunk: diffHunk{lines: []string{" a"}}},
		{name: "context mismatch", lines: []string{"a"}, hunk: diffHunk{lines: []string{" b"}}},
		{name: "delete beyond end of file", lines: nil, hunk: diffHunk{lines: []string{"-a"}}},
		{name: "delete mismatch", lines: []string{"a"}, hunk: diffHunk{lines: []string{"-b"}}},
		{name: "invalid prefix", lines: []string{"a"}, hunk: diffHunk{lines: []string{"!a"}}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := applyHunkAt(tc.lines, 0, tc.hunk); err == nil {
				t.Fatalf("applyHunkAt accepted %s", tc.name)
			}
		})
	}
}

func TestApplyDiffTrimsTrailingNewlineAtEndOfFile(t *testing.T) {
	got, err := applyDiff("a\nb\n", []string{" a", "-b", "+B"}, true)
	if err != nil {
		t.Fatalf("applyDiff: %v", err)
	}
	if got != "a\nB" {
		t.Fatalf("applyDiff = %q, want the trailing newline trimmed", got)
	}
}

func TestParseUnifiedDiffRangeErrors(t *testing.T) {
	t.Run("old range", func(t *testing.T) {
		if got, err := parseUnifiedDiffOldRange("@@"); err != nil || got != 0 {
			t.Fatalf("bare header = %d, %v; want 0, nil", got, err)
		}
		if _, err := parseUnifiedDiffOldRange("@@ nope"); err == nil {
			t.Error("header without an old range was accepted")
		}
		if _, err := parseUnifiedDiffOldRange("@@ -x,1"); err == nil {
			t.Error("non-numeric start was accepted")
		}
		if _, err := parseUnifiedDiffOldRange("@@ -1,x"); err == nil {
			t.Error("non-numeric count was accepted")
		}
		if got, err := parseUnifiedDiffOldRange("@@ -12,4"); err != nil || got != 12 {
			t.Fatalf("header = %d, %v; want 12, nil", got, err)
		}
	})

	t.Run("count-only range", func(t *testing.T) {
		if got, err := parseUnifiedDiffRange("7"); err != nil || got != 7 {
			t.Fatalf("range = %d, %v; want 7, nil", got, err)
		}
	})
}

func TestApplyPatchOperationErrors(t *testing.T) {
	t.Run("unknown kind", func(t *testing.T) {
		if _, err := applyPatchOperation(patchOperation{kind: "nope"}); err == nil {
			t.Fatal("applyPatchOperation accepted an unknown kind")
		}
	})

	t.Run("add file line without a plus", func(t *testing.T) {
		if _, err := applyAddFile(patchOperation{path: "x", diffLines: []string{"no plus"}}); err == nil {
			t.Fatal("applyAddFile accepted a line without a plus")
		}
	})

	t.Run("add file under a regular file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatalf("write blocker: %v", err)
		}
		op := patchOperation{path: filepath.Join(blocker, "child", "f"), diffLines: []string{"+hi"}}
		if _, err := applyAddFile(op); err == nil {
			t.Fatal("applyAddFile created a file under a regular file")
		}
	})

	t.Run("update file that cannot be read", func(t *testing.T) {
		if _, err := applyUpdateFile(patchOperation{path: filepath.Join(t.TempDir(), "absent")}); err == nil {
			t.Fatal("applyUpdateFile read a missing file")
		}
	})

	t.Run("delete file that cannot be removed", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "child"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write child: %v", err)
		}
		if _, err := applyDeleteFile(patchOperation{path: dir}); err == nil {
			t.Fatal("applyDeleteFile removed a non-empty directory")
		}
	})
}

func TestHunkMatchesAtRejectsUnknownPrefix(t *testing.T) {
	if hunkMatchesAt([]string{"a"}, 0, diffHunk{lines: []string{"!a"}}) {
		t.Fatal("hunkMatchesAt accepted an unknown prefix")
	}
}
