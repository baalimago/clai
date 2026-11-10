package capture

import "os"

// interruptProcess and killProcess signal the started process and no other.
// They are the stop of a platform whose ffmpeg may be a wrapper script around
// a program that cannot take a signal: the wrapper alone is told to stop, and
// it ends its child's inputs so that the child finishes the file.

func interruptProcess(process *os.Process) error {
	return process.Signal(os.Interrupt)
}

func killProcess(process *os.Process) error {
	return process.Kill()
}
