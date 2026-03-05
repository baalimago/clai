package tools

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	scanner := bufio.NewScanner(strings.NewReader(patch))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse apply_patch scan: %w", err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("parse apply_patch: %w", errors.New("patch is empty"))
	}
	if strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("parse apply_patch: %w", errors.New("missing Begin Patch marker"))
	}

	var ops []patchOperation
	for i := 1; i < len(lines); {
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
	for _, line := range diffLines {
		if strings.HasPrefix(line, "@@") {
			continue
		}
		if line == "" {
			return "", fmt.Errorf("apply diff: %w", errors.New("diff line missing prefix"))
		}
		if strings.TrimSpace(line) == "*** End of File" {
			endOfFile = true
			continue
		}
		prefix := line[0]
		content := line[1:]
		switch prefix {
		case ' ':
			if idx >= len(origLines) {
				return "", fmt.Errorf("apply diff: %w", errors.New("context beyond end of file"))
			}
			if origLines[idx] != content {
				return "", fmt.Errorf("apply diff: %w", fmt.Errorf("context mismatch: expected %q, got %q", origLines[idx], content))
			}
			out = append(out, content)
			idx++
		case '-':
			if idx >= len(origLines) {
				return "", fmt.Errorf("apply diff: %w", errors.New("delete beyond end of file"))
			}
			if origLines[idx] != content {
				return "", fmt.Errorf("apply diff: %w", fmt.Errorf("delete mismatch: expected %q, got %q", origLines[idx], content))
			}
			idx++
		case '+':
			out = append(out, content)
		default:
			return "", fmt.Errorf("apply diff: %w", fmt.Errorf("invalid diff prefix %q", string(prefix)))
		}
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
