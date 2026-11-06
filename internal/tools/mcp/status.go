package mcp

import (
	pub_models "github.com/baalimago/clai/pkg/text/models"

	"github.com/baalimago/clai/internal/tools/mcp/launcher"
)

// ConnectStatusSink is told when a server's connect begins and, through the
// returned function, when it ends. A sink that draws a live region uses it to
// show which server clai is waiting on and for how long, so a multi-minute
// first-run install reads as work in progress rather than as a stall. It is
// discovered by an optional-interface check, the same shape as AuthPendingSink
// and setupSucceeded.
type ConnectStatusSink interface {
	ConnectStarted(server, label string) (done func())
}

// AnnounceConnect tells sink that server's connect is starting and returns the
// function that reports it finished. A sink that shows nothing gets a no-op, so
// every connect site announces unconditionally.
func AnnounceConnect(sink ServerLogSink, server pub_models.McpServer) (done func()) {
	cs, ok := sink.(ConnectStatusSink)
	if !ok {
		return func() {}
	}
	return cs.ConnectStarted(server.Name, connectLabel(server))
}

// connectLabel names what the wait is for: an install that may download its
// package, or a plain start. The package is named only when the command line
// spells it out, so the label never guesses.
func connectLabel(server pub_models.McpServer) string {
	probe := launcher.ProbeInstall(server)
	if !probe.Need.NeedsInstall() {
		return "starting"
	}
	if probe.Target == "" {
		return "installing"
	}
	return "installing " + probe.Target
}
