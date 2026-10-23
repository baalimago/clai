package testenv

import (
	"runtime"
	"testing"
)

func TestLimitProcs_capsGOMAXPROCSAndReturnsPrevious(t *testing.T) {
	before := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(before) })

	got := LimitProcs()

	if int(got) != before {
		t.Fatalf("LimitProcs() = %d, want the previous setting %d", got, before)
	}
	if now := runtime.GOMAXPROCS(0); now != Procs {
		t.Fatalf("GOMAXPROCS after LimitProcs = %d, want %d", now, Procs)
	}
}
