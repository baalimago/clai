package cloudcmd

import (
	"errors"
	"strings"
	"testing"
)

func TestUnknownSubcommand_failsWithTheList(t *testing.T) {
	h := newHarness(t, "")
	if code, _ := h.run("bogus"); code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	err := Command(h.deps()).CheckArgs([]string{"bogus"})
	var unknown *UnknownSubcommandError
	if !errors.As(err, &unknown) || unknown.Name != "bogus" {
		t.Fatalf("err = %v", err)
	}
	mustContain(t, err.Error(), `unknown cloud subcommand "bogus"`, "login", "status", "commit", "clai cloud -h")
}

func TestCheckArgs_reportsAFlagErrorWithTheSubcommandsUsage(t *testing.T) {
	c := Command(newHarness(t, "").deps())
	for _, args := range [][]string{{"status", "-zz"}, {"log", "-limit", "abc"}, {"pull", "config", "-nope"}} {
		err := c.CheckArgs(args)
		var flagErr *FlagError
		if !errors.As(err, &flagErr) || flagErr.Sub != args[0] {
			t.Fatalf("%v: err = %v", args, err)
		}
		mustContain(t, err.Error(), "cloud "+args[0], "Flags:")
		if strings.Contains(err.Error(), "unknown error") || strings.Contains(err.Error(), "(c)ommand (l)ine") {
			t.Fatalf("%v: %s", args, err)
		}
	}
	for _, args := range [][]string{nil, {"status"}, {"pull", "config", "-theirs"}, {"status", "-h"}, {"-x"}, {"log", "-limit", "5"}} {
		if err := c.CheckArgs(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t, "tok-a")
	for _, args := range [][]string{
		{"log", "-limit", "0"},
		{"log", "-limit", "-3"},
		{"whoami", "extra"},
		{"logout", "extra"},
		{"login", "extra"},
	} {
		code, out := h.run(args...)
		if code != 1 {
			t.Errorf("%v: code %d: %s", args, code, out)
		}
	}
	if h.read("cloud/credentials.json") == "" {
		t.Fatal("logout with an extra argument logged out")
	}
}

func TestRawHelp_saysJSON(t *testing.T) {
	c := Command(newHarness(t, "").deps())
	for _, name := range []string{"whoami", "status", "pull", "commit", "log"} {
		help := c.Subs[name].(interface{ Help() string }).Help()
		if !strings.Contains(help, "print JSON") || strings.Contains(help, "glow") {
			t.Errorf("%s -r help:\n%s", name, help)
		}
	}
}
