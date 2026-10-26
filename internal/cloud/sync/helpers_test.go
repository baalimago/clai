package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.MainInHome(m) }

var testNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

type machine struct {
	t *testing.T
	*Engine
}

func newMachine(t *testing.T, srv *cloudtest.Server, token string) machine {
	t.Helper()
	return machine{t: t, Engine: &Engine{Client: cloud.NewClient(srv.URL(), token), ConfDir: t.TempDir(), Now: func() time.Time { return testNow }}}
}

func (m machine) write(rel, content string) {
	m.t.Helper()
	p := m.abs(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

func (m machine) read(rel string) string {
	m.t.Helper()
	b, err := os.ReadFile(m.abs(rel))
	if err != nil {
		m.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (m machine) exists(rel string) bool {
	_, err := os.Stat(m.abs(rel))
	return err == nil
}

func (m machine) pull(store Store, opts PullOptions) PullResult {
	m.t.Helper()
	res, err := m.Pull(context.Background(), store, opts)
	if err != nil {
		m.t.Fatalf("pull %s: %v", store, err)
	}
	return res
}

func (m machine) commit(store Store, msg string) CommitResult {
	m.t.Helper()
	res, err := m.Commit(context.Background(), store, CommitOptions{Message: msg})
	if err != nil {
		m.t.Fatalf("commit %s: %v", store, err)
	}
	return res
}
