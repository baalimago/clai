package main

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type goldenFileTestCase struct {
	expect          string
	givenArgs       string
	givenEnvs       map[string]string
	wantOutExactly  string
	wantOutContains string
	wantErr         string
	wantStatusCode  int
}

// Test_goldenFile_calibration of the golden file tests to ensure they work
func Test_goldenFile_calibration(t *testing.T) {
	tcs := []goldenFileTestCase{
		{
			expect: "base-test",
			// These tests work by using the `test` chat model which is an
			// echo text querier. It will respond with whatever the input is
			givenArgs:      "-r -cm test q test",
			givenEnvs:      make(map[string]string),
			wantOutExactly: "test\n",
			wantErr:        "",
			wantStatusCode: 0,
		},
		{
			// This is a bit meta to ensure the goldenfile tests work
			expect:         "Multiple tests-test",
			givenArgs:      "-r -cm test q another test",
			givenEnvs:      make(map[string]string),
			wantOutExactly: "another test\n",
			wantErr:        "",
			wantStatusCode: 0,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.expect, func(t *testing.T) {
			oldArgs := os.Args
			t.Cleanup(func() {
				os.Args = oldArgs
			})

			for k, v := range tc.givenEnvs {
				t.Setenv(k, v)
			}
			var gotStatusCode int
			gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
				gotStatusCode = run(strings.Split(tc.givenArgs, " "))
			})

			testboil.FailTestIfDiff(t, gotStatusCode, tc.wantStatusCode)
			if tc.wantOutContains != "" {
				testboil.AssertStringContains(t, gotStdout, tc.wantOutContains)
			}
			if tc.wantOutExactly != "" {
				testboil.FailTestIfDiff(t, gotStdout, tc.wantOutExactly)
			}
		})
	}
}

func Test_goldenFile_CHAT_DIRSCOPED(t *testing.T) {
	// IMPLEMENT ME
}
