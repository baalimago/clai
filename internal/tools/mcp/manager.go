package mcp

import (
	"context"

	"github.com/baalimago/clai/internal/tools"
)

func Manager(ctx context.Context, controlChannel <-chan ControlEvent, statusChan chan<- error) {
	defer close(statusChan)
	for {
		select {
		case <-ctx.Done():
			statusChan <- ctx.Err()
			return
		case ev, ok := <-controlChannel:
			if !ok {
				statusChan <- nil
				return
			}
			go handleServer(ev)
		}
	}
}

func handleServer(ev ControlEvent) {
	msg, ok := <-ev.OutputChan
	if !ok {
		return
	}
	specs, ok := msg.([]tools.Specification)
	if !ok {
		return
	}
	for _, spec := range specs {
		tools.Tools.Register(NewToolWrapper(spec, ev.InputChan, ev.OutputChan))
	}
}
