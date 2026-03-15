package main

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_QUERY_raw_output_ends_with_newline_before_bell(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.FailTestIfDiff(t, stdout, "hello\n\a")
}
