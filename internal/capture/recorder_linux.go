//go:build linux

package capture

func newRecorder() Recorder { return recorder{} }
