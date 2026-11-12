package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// jsonListingContext is a JSON-format run over buffers, as the composition
// root builds it: stdout is the record wrapper unless a command asks for the
// unwrapped writer.
func jsonListingContext(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return runenv.With(context.Background(), runenv.New(runenv.Config{
		Format:  jsonl.FormatJSON,
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdout: &stdout, Stderr: &stderr}),
	})), &stdout
}

type listedTool struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Inputs      *pub_models.InputSchema `json:"input_schema"`
	Aliases     []string                `json:"aliases"`
	Dependency  string                  `json:"dependency"`
	Available   bool                    `json:"available"`
}

func TestListJSONWritesTheFullListingAsOneDocument(t *testing.T) {
	ctx, stdout := jsonListingContext(t)
	description := "a description far longer than any terminal width allows, kept whole so a script reads the same text a model would"
	spec := pub_models.Specification{
		Name:        "canon",
		Description: description,
		Inputs: &pub_models.InputSchema{
			Type: "object",
			Properties: map[string]pub_models.ParameterObject{
				"path": {Type: "string", Description: "where"},
			},
		},
	}

	WithTestRegistry(t, func() {
		Registry.Set("canon", &mockLLMTool{spec: spec})
		Registry.SetAlias("old-name", "canon", &mockLLMTool{spec: spec})

		if err := List(ctx); err != nil {
			t.Fatalf("List: %v", err)
		}
	})

	if strings.Contains(stdout.String(), `"type":"output"`) {
		t.Fatalf("the JSON listing is still the record stream:\n%s", stdout.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("the JSON listing is not one document:\n%s", stdout.String())
	}
	var got []listedTool
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal the listing: %v\n%s", err, stdout.String())
	}
	var canon *listedTool
	for i := range got {
		if got[i].Name == "canon" {
			canon = &got[i]
		}
	}
	if canon == nil {
		t.Fatalf("the listing holds no canon: %+v", got)
	}
	if canon.Description != description {
		t.Fatalf("got %+v, want the full canonical specification", canon)
	}
	if canon.Inputs == nil || canon.Inputs.Properties["path"].Description != "where" {
		t.Fatalf("input schema lost: %+v", canon.Inputs)
	}
	if len(canon.Aliases) != 1 || canon.Aliases[0] != "old-name" {
		t.Fatalf("aliases = %v, want [old-name]", canon.Aliases)
	}
}

func TestDetailJSONWritesTheSpecificationAsOneDocument(t *testing.T) {
	ctx, stdout := jsonListingContext(t)
	spec := pub_models.Specification{Name: "canon", Description: "canonical description"}

	WithTestRegistry(t, func() {
		Registry.Set("canon", &mockLLMTool{spec: spec})

		if err := Detail(ctx, "canon"); err != nil {
			t.Fatalf("Detail: %v", err)
		}
	})

	var got pub_models.Specification
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal the detail: %v\n%s", err, stdout.String())
	}
	if got.Name != "canon" || got.Description != "canonical description" {
		t.Fatalf("got %+v, want the full specification", got)
	}
	if strings.Contains(stdout.String(), `"type":"output"`) {
		t.Fatalf("the JSON detail is still the record stream:\n%s", stdout.String())
	}
}
