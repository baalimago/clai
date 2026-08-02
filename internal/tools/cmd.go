package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/table"
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
		return table.ErrUserInitiatedExit
	}

	tls := Registry.All()
	aliases := Registry.Aliases()
	var toolNames []string
	for k := range tls {
		if _, isAlias := aliases[k]; !isAlias {
			toolNames = append(toolNames, k)
		}
	}
	sort.Strings(toolNames)

	fmt.Printf("Available Tools:\n")
	for _, name := range toolNames {
		tool := tls[name]
		spec := tool.Specification()
		aliasSuffix := ""
		var aliasesOfTool []string
		for alias, canonical := range aliases {
			if canonical == name {
				aliasesOfTool = append(aliasesOfTool, alias)
			}
		}
		if len(aliasesOfTool) > 0 {
			sort.Strings(aliasesOfTool)
			aliasSuffix = " (alias: " + strings.Join(aliasesOfTool, ", ") + ")"
		}
		prefix := fmt.Sprintf("- %s%s: ", name, aliasSuffix)
		maybeShortenedDesc, err := table.WidthAppropriateStringTrunc(spec.Description, prefix, 5)
		if err != nil {
			return fmt.Errorf("failed to truncate descriptoin: :%v", err)
		}
		fmt.Println(maybeShortenedDesc)
	}
	fmt.Println("\nRun 'clai tools <tool-name>' for more details.")
	return table.ErrUserInitiatedExit
}
