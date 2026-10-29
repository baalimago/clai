//go:build darwin

package capture

import (
	"context"
	"fmt"
	"os/exec"
)

func sources(ctx context.Context, ffmpeg string) ([]Source, error) {
	args := []string{"-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", ""}
	out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	list, parseErr := parseAVFoundationDevices(string(out))
	if parseErr == nil {
		return list, nil
	}
	if err != nil {
		if ctxErr := discoveryCancellation(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, childFailure(fmt.Sprintf("%v: ffmpeg -f avfoundation -list_devices failed", parseErr), err, string(out))
	}
	return nil, parseErr
}
