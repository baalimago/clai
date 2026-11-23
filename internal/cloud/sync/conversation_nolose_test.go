package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

func TestPullConversation_failedWriteLosesNoLocalMessage(t *testing.T) {
	blockWithDir := func(t *testing.T, m machine, rel string) {
		t.Helper()
		m.write(rel+"/keep", "x")
	}
	t.Run("trash fails, X keeps its local copy", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a", "b", "c"}))
		m := newMachine(t, srv, "tok-b")
		local := chatJSON("X", []string{"a"})
		m.write(convPath("X"), local)
		m.write("cloud/trash", "not a directory")

		if _, err := m.PullConversation(context.Background(), "X"); err == nil {
			t.Fatal("want an error")
		}
		if m.read(convPath("X")) != local {
			t.Fatal("the local X was changed")
		}
	})
	t.Run("fork write fails, X keeps its local copy", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}))
		m := newMachine(t, srv, "tok-b")
		local := chatJSON("X", []string{"a", "b"})
		m.write(convPath("X"), local)
		m.newID = func() (string, error) { return "L", nil }
		blockWithDir(t, m, convPath("L"))

		if _, err := m.PullConversation(context.Background(), "X"); err == nil {
			t.Fatal("want an error")
		}
		if m.read(convPath("X")) != local {
			t.Fatal("the local X was changed")
		}
	})
	t.Run("fork sidecar write fails, X keeps its local copy", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}))
		m := newMachine(t, srv, "tok-b")
		local := chatJSON("X", []string{"a", "b"})
		m.write(convPath("X"), local)
		m.write("conversations/reasoning/X/c1.json", `[1]`)
		m.newID = func() (string, error) { return "L", nil }
		m.write("conversations/reasoning/L", "not a directory")

		if _, err := m.PullConversation(context.Background(), "X"); err == nil {
			t.Fatal("want an error")
		}
		if m.read(convPath("X")) != local {
			t.Fatal("the local X was changed")
		}
		if m.exists(convPath("L")) {
			t.Fatal("a fork was written without its sidecars")
		}
	})
	t.Run("X.json write fails, the old X is in the trash", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a", "b", "c"}))
		m := newMachine(t, srv, "tok-b")
		local := chatJSON("X", []string{"a"})
		m.write(convPath("X"), local)
		trashed := trashFile(m, convPath("X"))
		m.Now = func() time.Time {
			if _, err := os.Stat(trashed); err == nil {
				if err := os.MkdirAll(filepath.Join(m.abs(convPath("X")), "keep"), 0o755); err != nil {
					t.Error(err)
				}
			}
			return testNow
		}

		if _, err := m.PullConversation(context.Background(), "X"); err == nil {
			t.Fatal("want an error")
		}
		if b, _ := os.ReadFile(trashed); string(b) != local {
			t.Fatalf("trash holds %q", b)
		}
	})
}
