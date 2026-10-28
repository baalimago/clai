//go:build darwin

package capture

func newRecorder() Recorder { return recorder{} }
