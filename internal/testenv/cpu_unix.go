//go:build unix

package testenv

import (
	"fmt"
	"syscall"
	"time"
)

// processCPU is the user and system time the process has consumed.
func processCPU() (time.Duration, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, fmt.Errorf("getrusage: %w", err)
	}
	return timevalDuration(ru.Utime) + timevalDuration(ru.Stime), nil
}

func timevalDuration(tv syscall.Timeval) time.Duration {
	return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
}
