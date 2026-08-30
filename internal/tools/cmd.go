package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// Detail prints the specification of one tool.
func Detail(toolName string) error {
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
	return nil
}

// List prints every available tool with its aliases and description.
func List() error {
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
		// The listing writes to stdout, so it resolves one snapshot bound to
		// stdout's fd (R2-02): a non-terminal stdout yields the deterministic
		// fallback width.
		maybeShortenedDesc := table.WidthAppropriateStringTruncWithWidth(spec.Description, prefix, 5, utils.SessionDimensions(os.Stdout).Width)
		fmt.Println(maybeShortenedDesc)
	}
	fmt.Println("\nRun 'clai tools <tool-name>' for more details.")
	return nil
}

// Command builds the tools command tree.
func Command() *internal.Command {
	nonInteractive := &internal.NonInteractiveFlag{}
	c := &internal.Command{
		Name:           "tools",
		Desc:           "List available tools, or show details for a specific tool",
		HelpText:       "tools [tool name]. Lists mcp and built-in tools, or one tool's specification.",
		Register:       nonInteractive.Register,
		NonInteractive: nonInteractive,
		CompleteArgsFn: toolNameArgs,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		Init()
		if args := c.Args(); len(args) > 1 {
			return Detail(args[1])
		}
		return List()
	}
	toolsList := &internal.Command{
		Name: "list",
		Desc: "List available tools, both mcp and built-in",
		HelpText: `tools list. Lists every available tool.

Examples:
  clai tools list`,
	}
	toolsList.OnRun = func(_ context.Context, _ *internal.Command) error {
		Init()
		return List()
	}
	c.Subs = map[string]cmd.Command{"list": toolsList}
	return c
}

// toolNameArgs completes the tools command's detail-view positional.
func toolNameArgs(args []string, partial string) []cmd.CompletionItem {
	if len(args) > 0 {
		return []cmd.CompletionItem{}
	}
	return internal.PlainItems(partial, Names())
}
