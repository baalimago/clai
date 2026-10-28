package capture

import "runtime"

func supportedFor(goos string) bool {
	return goos == "linux" || goos == "darwin"
}

func supportedForGOOS() bool { return supportedFor(runtime.GOOS) }
