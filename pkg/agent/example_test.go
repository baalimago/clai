package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

// Review is the shape a consumer extracts from a model answer.
type Review struct {
	Verdict string `json:"verdict"`
	Score   int    `json:"score"`
}

// Example_queryTypedReview runs a typed query against the built-in mock
// vendor, which echoes the last user message as its reply. The example needs
// no API key and no network, so the test suite executes it on every run.
func Example_queryTypedReview() {
	// The agent owns its config directory: WithConfigDir appends "clai"
	// unless the path already ends with it, and NewQuerier reads and writes
	// <vendor>_<model>_<version>.json there. Seeding that file with a price
	// entry keeps the cost manager off the network, so the run prints
	// nothing except the example output. The file name is vendorType's: the
	// mock vendor with model "mock" and version "mock". Most consumers never
	// write this file; the example must, to run without a price fetch.
	root, err := os.MkdirTemp("", "clai-example")
	if err != nil {
		fmt.Println("temp dir:", err)
		return
	}
	defer os.RemoveAll(root)
	cfgDir := filepath.Join(root, "clai")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		fmt.Println("config dir:", err)
		return
	}
	price, err := json.Marshal(map[string]any{
		"price": map[string]any{
			"input_usd_per_token":  0.001,
			"output_usd_per_token": 0.002,
		},
	})
	if err != nil {
		fmt.Println("price config:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "mock_mock_mock.json"), price, 0o644); err != nil {
		fmt.Println("price file:", err)
		return
	}

	querier := NewTyped[Review](
		WithModel("mock_test"),
		WithPrompt("You review pull requests."),
		WithConfigDir(root),
	)
	ctx := context.Background()
	if err := querier.Setup(ctx); err != nil {
		fmt.Println("setup:", err)
		return
	}

	// The prompt doubles as the payload the mock echoes, so it is valid JSON.
	review, err := querier.Query(ctx, models.Chat{
		Created:  time.Now(),
		ID:       "readme-example",
		Messages: []models.Message{{Role: "user", Content: `{"verdict":"ship","score":9}`}},
	})
	if err != nil {
		fmt.Println("query:", err)
		return
	}
	fmt.Println(review.Verdict, review.Score)
	// Output:
	// ship 9
}
