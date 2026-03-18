package tools

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type ApplyPatchTool pub_models.Specification

type patchOperation struct {
	kind       string
	path       string
	moveTo     string
	diffLines  []string
	endOfFile  bool
	lineNumber int
}

type diffHunk struct {
	oldStart int
	lines    []string
}

const (
	patchKindAdd    = "add"
	patchKindUpdate = "update"
	patchKindDelete = "delete"
)

var ApplyPatch = ApplyPatchTool{
	Name:        "apply_patch",
	Description: "Apply a patch to files. Supports adding, updating, deleting, and moving files using the apply_patch format.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"patch": {
				Type:        "string",
				Description: "The apply_patch formatted patch text.",
			},
		},
		Required: []string{"patch"},
	},
}

func (a ApplyPatchTool) Call(input pub_models.Input) (string, error) {
	patch, ok := input["patch"].(string)
	if !ok {
		return "", fmt.Errorf("apply_patch call: %w", errors.New("patch must be a string"))
	}
	if strings.TrimSpace(patch) == "" {
		return "", fmt.Errorf("apply_patch call: %w", errors.New("patch must be non-empty"))
	}

	ops, err := parseApplyPatch(patch)
	if err != nil {
		return "", fmt.Errorf("apply_patch call parse: %w", err)
	}

	var outputs []string
	for _, op := range ops {
		out, err := applyPatchOperation(op)
		if err != nil {
			return "", fmt.Errorf("apply_patch call apply operation at line %d: %w", op.lineNumber, err)
		}
		if out != "" {
			outputs = append(outputs, out)
		}
	}

	return strings.Join(outputs, "\n"), nil
}

func (a ApplyPatchTool) Specification() pub_models.Specification {
	return pub_models.Specification(ApplyPatch)
}

func parseApplyPatch(patch string) ([]patchOperation, error) {
	lines, err := patchLines(patch)
	if err != nil {
		return nil, fmt.Errorf("parse apply_patch scan: %w", err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("parse apply_patch: %w", errors.New("patch is empty"))
	}

	beginIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "*** Begin Patch" {
			beginIndex = i
			break
		}
	}
	if beginIndex == -1 {
		return nil, fmt.Errorf("parse apply_patch: %w", errors.New("missing Begin Patch marker"))
	}

	var ops []patchOperation
	for i := beginIndex + 1; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line) == "*** End Patch" {
			return ops, nil
		}
		if after, ok := strings.CutPrefix(line, "*** Add File: "); ok {
			path := strings.TrimSpace(after)
			if path == "" {
				return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("missing add file path at line %d", i+1))
			}
			op := patchOperation{kind: patchKindAdd, path: path, lineNumber: i + 1}
			i++
			for i < len(lines) {
				if strings.HasPrefix(lines[i], "*** ") {
					break
				}
				op.diffLines = append(op.diffLines, lines[i])
				i++
			}
			if len(op.diffLines) == 0 {
				return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("add file missing content at line %d", op.lineNumber))
			}
			ops = append(ops, op)
			continue
		}
		if after, ok := strings.CutPrefix(line, "*** Delete File: "); ok {
			path := strings.TrimSpace(after)
			if path == "" {
				return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("missing delete file path at line %d", i+1))
			}
			op := patchOperation{kind: patchKindDelete, path: path, lineNumber: i + 1}
			i++
			ops = append(ops, op)
			continue
		}
		if after, ok := strings.CutPrefix(line, "*** Update File: "); ok {
			path := strings.TrimSpace(after)
			if path == "" {
				return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("missing update file path at line %d", i+1))
			}
			op := patchOperation{kind: patchKindUpdate, path: path, lineNumber: i + 1}
			i++
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				if op.moveTo == "" {
					return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("missing move to path at line %d", i+1))
				}
				i++
			}
			for i < len(lines) {
				if strings.HasPrefix(lines[i], "*** ") {
					break
				}
				if strings.TrimSpace(lines[i]) == "*** End of File" {
					op.endOfFile = true
					i++
					continue
				}
				op.diffLines = append(op.diffLines, lines[i])
				i++
			}
			if len(op.diffLines) == 0 {
				return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("update file missing diff at line %d", op.lineNumber))
			}
			ops = append(ops, op)
			continue
		}
		return nil, fmt.Errorf("parse apply_patch: %w", fmt.Errorf("unexpected patch line at %d: %s", i+1, line))
	}

	return nil, fmt.Errorf("parse apply_patch: %w", errors.New("missing End Patch marker"))
}

func patchLines(patch string) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(patch))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan patch lines: %w", err)
	}
	return lines, nil
}

func applyPatchOperation(op patchOperation) (string, error) {
	switch op.kind {
	case patchKindAdd:
		return applyAddFile(op)
	case patchKindUpdate:
		return applyUpdateFile(op)
	case patchKindDelete:
		return applyDeleteFile(op)
	default:
		return "", fmt.Errorf("apply patch operation: %w", fmt.Errorf("unknown patch operation kind: %s", op.kind))
	}
}

func applyAddFile(op patchOperation) (string, error) {
	var contentLines []string
	for _, line := range op.diffLines {
		if !strings.HasPrefix(line, "+") {
			return "", fmt.Errorf("apply add file: %w", fmt.Errorf("line must start with '+': %q", line))
		}
		contentLines = append(contentLines, strings.TrimPrefix(line, "+"))
	}
	content := strings.Join(contentLines, "\n")
	if err := os.MkdirAll(filepath.Dir(op.path), 0o755); err != nil {
		return "", fmt.Errorf("apply add file create dirs for %s: %w", op.path, err)
	}
	if err := os.WriteFile(op.path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("apply add file write %s: %w", op.path, err)
	}
	return fmt.Sprintf("Created %s", op.path), nil
}

func applyUpdateFile(op patchOperation) (string, error) {
	original, err := os.ReadFile(op.path)
	if err != nil {
		return "", fmt.Errorf("apply update file read %s: %w", op.path, err)
	}
	updated, err := applyDiff(string(original), op.diffLines, op.endOfFile)
	if err != nil {
		return "", fmt.Errorf("apply update file diff %s: %w", op.path, err)
	}

	targetPath := op.path
	if op.moveTo != "" {
		targetPath = op.moveTo
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", fmt.Errorf("apply update file create dirs for %s: %w", targetPath, err)
		}
	}

	if err := os.WriteFile(targetPath, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("apply update file write %s: %w", targetPath, err)
	}

	if op.moveTo != "" {
		if err := os.Remove(op.path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("apply update file remove original %s: %w", op.path, err)
		}
		return fmt.Sprintf("Moved %s to %s", op.path, op.moveTo), nil
	}

	return fmt.Sprintf("Updated %s", op.path), nil
}

func applyDeleteFile(op patchOperation) (string, error) {
	if err := os.Remove(op.path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("apply delete file %s: %w", op.path, err)
	}
	return fmt.Sprintf("Deleted %s", op.path), nil
}

func applyDiff(original string, diffLines []string, endOfFile bool) (string, error) {
	origLines := strings.Split(original, "\n")
	idx := 0
	out := make([]string, 0, len(origLines))

	hunks, err := splitDiffIntoHunks(diffLines)
	if err != nil {
		return "", fmt.Errorf("apply diff split hunks: %w", err)
	}

	for _, hunk := range hunks {
		matchIdx, err := findHunkStart(origLines, idx, hunk)
		if err != nil {
			return "", fmt.Errorf("apply diff find hunk start: %w", err)
		}
		out = append(out, origLines[idx:matchIdx]...)
		updatedLines, consumed, err := applyHunkAt(origLines, matchIdx, hunk)
		if err != nil {
			return "", fmt.Errorf("apply diff apply hunk at %d: %w", matchIdx, err)
		}
		out = append(out, updatedLines...)
		idx = matchIdx + consumed
	}
	if idx < len(origLines) {
		out = append(out, origLines[idx:]...)
	}
	result := strings.Join(out, "\n")
	if endOfFile {
		result = strings.TrimSuffix(result, "\n")
	}
	return result, nil
}

func splitDiffIntoHunks(diffLines []string) ([]diffHunk, error) {
	var hunks []diffHunk
	current := diffHunk{}
	haveCurrent := false

	flush := func() error {
		if !haveCurrent {
			return nil
		}
		if len(current.lines) == 0 {
			return fmt.Errorf("empty hunk")
		}
		hunks = append(hunks, current)
		current = diffHunk{}
		haveCurrent = false
		return nil
	}

	for _, line := range diffLines {
		if strings.TrimSpace(line) == "*** End of File" {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			if err := flush(); err != nil {
				return nil, fmt.Errorf("flush previous hunk: %w", err)
			}
			oldStart, err := parseUnifiedDiffOldRange(line)
			if err != nil {
				return nil, fmt.Errorf("parse hunk header %q: %w", line, err)
			}
			current = diffHunk{oldStart: oldStart}
			haveCurrent = true
			continue
		}
		if line == "" {
			return nil, fmt.Errorf("diff line missing prefix: %w", errors.New("empty line"))
		}
		prefix := line[0]
		if prefix != ' ' && prefix != '+' && prefix != '-' {
			return nil, fmt.Errorf("invalid diff prefix %q", string(prefix))
		}
		if !haveCurrent {
			current = diffHunk{}
			haveCurrent = true
		}
		current.lines = append(current.lines, line)
	}

	if err := flush(); err != nil {
		return nil, fmt.Errorf("flush final hunk: %w", err)
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("no hunks found")
	}
	return hunks, nil
}

func findHunkStart(lines []string, startIdx int, hunk diffHunk) (int, error) {
	if hunk.oldStart > 0 {
		targetIdx := hunk.oldStart - 1
		if targetIdx >= startIdx && targetIdx <= len(lines) && hunkMatchesAt(lines, targetIdx, hunk) {
			return targetIdx, nil
		}
	}
	for i := startIdx; i <= len(lines); i++ {
		if hunkMatchesAt(lines, i, hunk) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no matching hunk position from line %d", startIdx+1)
}

func hunkMatchesAt(lines []string, start int, hunk diffHunk) bool {
	idx := start
	for _, line := range hunk.lines {
		content := line[1:]
		switch line[0] {
		case ' ', '-':
			if idx >= len(lines) || lines[idx] != content {
				return false
			}
			idx++
		case '+':
			continue
		default:
			return false
		}
	}
	return true
}

func applyHunkAt(lines []string, start int, hunk diffHunk) ([]string, int, error) {
	idx := start
	updated := make([]string, 0, len(hunk.lines))
	for _, line := range hunk.lines {
		content := line[1:]
		switch line[0] {
		case ' ':
			if idx >= len(lines) {
				return nil, 0, fmt.Errorf("context beyond end of file")
			}
			if lines[idx] != content {
				return nil, 0, fmt.Errorf("context mismatch: expected %q, got %q", lines[idx], content)
			}
			updated = append(updated, content)
			idx++
		case '-':
			if idx >= len(lines) {
				return nil, 0, fmt.Errorf("delete beyond end of file")
			}
			if lines[idx] != content {
				return nil, 0, fmt.Errorf("delete mismatch: expected %q, got %q", lines[idx], content)
			}
			idx++
		case '+':
			updated = append(updated, content)
		default:
			return nil, 0, fmt.Errorf("invalid diff prefix %q", string(line[0]))
		}
	}
	return updated, idx - start, nil
}

func parseUnifiedDiffOldRange(header string) (int, error) {
	if header == "@@" {
		return 0, nil
	}
	fields := strings.Fields(header)
	if len(fields) < 2 {
		return 0, fmt.Errorf("parse unified diff old range: %w", fmt.Errorf("invalid hunk header %q", header))
	}
	oldRangeField := fields[1]
	if !strings.HasPrefix(oldRangeField, "-") {
		return 0, fmt.Errorf("parse unified diff old range: %w", fmt.Errorf("missing old range in header %q", header))
	}
	return parseUnifiedDiffRange(strings.TrimPrefix(oldRangeField, "-"))
}

func parseUnifiedDiffRange(raw string) (int, error) {
	parts := strings.SplitN(raw, ",", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("parse unified diff range start %q: %w", raw, err)
	}
	if len(parts) == 2 {
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return 0, fmt.Errorf("parse unified diff range count %q: %w", raw, err)
		}
	}
	return start, nil
}
