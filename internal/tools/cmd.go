package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/baalimago/clai/internal/utils"
)

func SubCmd(ctx context.Context, args []string) error {
	if len(args) > 1 {
		toolName := args[1]
		tool, exists := Registry.Get(toolName)
		if !exists {
			return fmt.Errorf("tool '%s' not found", toolName)
		}
		spec := tool.Specification()
		jsonSpec, err := json.MarshalIndent(spec, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal tool specification: %w", err)
		}
		fmt.Printf("%s\n", string(jsonSpec))
		return utils.ErrUserInitiatedExit
	}

	tls := Registry.All()
	var toolNames []string
	for k := range tls {
		toolNames = append(toolNames, k)
	}
	sort.Strings(toolNames)

	fmt.Printf("Available Tools:\n")
	for _, name := range toolNames {
		tool := tls[name]
		spec := tool.Specification()
		prefix := fmt.Sprintf("- %s: ", name)

		maybeShortenedDesc, err := utils.WidthAppropriateStringTrunc(spec.Description, prefix, 5)
		if err != nil {
			return fmt.Errorf("failed to truncate descriptoin: :%v", err)
		}
		fmt.Println(maybeShortenedDesc)
	}
	fmt.Println("\nRun 'clai tools <tool-name>' for more details.")
	return utils.ErrUserInitiatedExit
}
