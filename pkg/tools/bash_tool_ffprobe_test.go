package tools

import (
	"slices"
	"testing"
)

func TestFFProbeSpecification(t *testing.T) {
	spec := FFProbe.Specification()

	if spec.Name != "ffprobe" {
		t.Errorf("expected name 'ffprobe', got %q", spec.Name)
	}

	if spec.Description == "" {
		t.Error("expected non-empty description")
	}

	if spec.Inputs == nil {
		t.Fatal("expected inputs to be defined")
	}

	if spec.Inputs.Type != "object" {
		t.Errorf("expected inputs type 'object', got %q", spec.Inputs.Type)
	}

	// Check required fields
	expectedRequired := []string{"file"}
	if len(spec.Inputs.Required) != len(expectedRequired) {
		t.Errorf("expected %d required fields, got %d", len(expectedRequired), len(spec.Inputs.Required))
	}

	for _, req := range expectedRequired {
		found := slices.Contains(spec.Inputs.Required, req)
		if !found {
			t.Errorf("expected required field %q not found", req)
		}
	}

	// Check that file parameter exists
	if _, exists := spec.Inputs.Properties["file"]; !exists {
		t.Error("expected 'file' parameter to exist")
	}
}
