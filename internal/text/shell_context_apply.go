package text

import (
	"context"
	"fmt"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// AppendShellContextIfConfigured inserts the rendered shell context block into prompt.
//
// Insertion rule:
//   - if rendered is non-empty: "<shell context>\n" + rendered + "\n</shell context>\n" +
//     <original-prompt-with-leading-whitespace-trimmed>
func AppendShellContextIfConfigured(ctx context.Context, configDir, shellContextName, prompt string, r ShellContextRenderer) (string, error) {
	name := strings.TrimSpace(shellContextName)
	if name == "" {
		return prompt, nil
	}

	def, err := LoadShellContextDefinition(configDir, name)
	if err != nil {
		return prompt, fmt.Errorf("load shell context definition: %w", err)
	}

	if r.Warnf == nil {
		r.Warnf = ancli.Warnf
	}
	rendered, err := r.Render(ctx, name, def)
	if err != nil {
		return prompt, fmt.Errorf("render shell context: %w", err)
	}
	rendered = strings.Trim(rendered, " \t\r")
	if rendered == "" {
		return prompt, nil
	}
	rendered = strings.TrimPrefix(rendered, "\n")

	prompt = strings.TrimLeft(prompt, " \t\r\n")
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	return "<shell context>\n" + rendered + "</shell context>\n" + prompt, nil
}
