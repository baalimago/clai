//go:build !unix

package testenv

import (
	"errors"
	"time"
)

func processCPU() (time.Duration, error) {
	return 0, errors.New("process CPU time is not measured on this platform")
}
