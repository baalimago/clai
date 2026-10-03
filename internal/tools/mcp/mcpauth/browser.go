package mcpauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// BrowserOpener opens url in the operator's browser. No such helper exists
// in the repository today (README code-layout table); its fake lives in
// internal/tools/mcp/oauthtestserver.
type BrowserOpener interface {
	Open(url string) error
}

// osBrowserOpener is the production BrowserOpener: it shells out to the
// platform's own "open a URL" command. Failure (including an unsupported
// platform) is returned, never panicked, so the interactive flow falls
// back to the printed-URL path.
type osBrowserOpener struct{}

// DefaultBrowserOpener returns the production BrowserOpener.
func DefaultBrowserOpener() BrowserOpener { return osBrowserOpener{} }

func (osBrowserOpener) Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcpauth: open browser: %w", err)
	}
	return nil
}
