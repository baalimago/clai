package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireMissing(t *testing.T, err error, want ...string) {
	t.Helper()
	var missing *serverconfig.MissingEnvError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want *serverconfig.MissingEnvError", err)
	}
	if !slices.Equal(missing.Missing, want) {
		t.Fatalf("Missing = %v, want %v", missing.Missing, want)
	}
}

func requireNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists (stat err %v): something was spawned", path, err)
	}
}

func TestSpawnEnvMergesProcessEnvfileAndEnvMapLastWins(t *testing.T) {
	t.Setenv("CLAI_RE_PROC", "proc")
	t.Setenv("CLAI_RE_OVERRIDE", "proc")
	server := pub_models.McpServer{
		Name:    "s",
		Command: "true",
		EnvFile: writeEnvFile(t, "CLAI_RE_FILE=file\nCLAI_RE_OVERRIDE=file\nCLAI_RE_FILE_ONLY=file\n"),
		Env:     map[string]string{"CLAI_RE_OVERRIDE": "map", "CLAI_RE_FILE_ONLY": ""},
	}
	env, err := spawnEnv(server)
	if err != nil {
		t.Fatalf("spawnEnv: %v", err)
	}
	lookup := envLookup(env)
	for name, want := range map[string]string{
		"CLAI_RE_PROC": "proc", "CLAI_RE_FILE": "file", "CLAI_RE_OVERRIDE": "map", "CLAI_RE_FILE_ONLY": "", "CLAI_RE_ABSENT": "",
	} {
		if got := lookup(name); got != want {
			t.Errorf("lookup(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSpawnEnvReportsUnreadableEnvfile(t *testing.T) {
	_, err := spawnEnv(pub_models.McpServer{Name: "s", Command: "true", EnvFile: filepath.Join(t.TempDir(), "absent.env")})
	if err == nil {
		t.Fatal("spawnEnv with a missing envfile: want an error")
	}
	_, err = NewStdioConn(t.Context(), pub_models.McpServer{Name: "s", Command: "true", EnvFile: filepath.Join(t.TempDir(), "absent.env")}, nil)
	if err == nil {
		t.Fatal("NewStdioConn with a missing envfile: want an error")
	}
}

func TestNewStdioConnRefusesMissingRequiredEnvBeforeSpawning(t *testing.T) {
	t.Setenv("CLAI_RE_EMPTIED", "set-in-process")
	marker := filepath.Join(t.TempDir(), "spawned")
	server := pub_models.McpServer{
		Name:    "local",
		Command: "sh",
		Args:    []string{"-c", "touch " + marker},
		// The env map's empty value wins over the process environment,
		// exactly as it would in the spawned process.
		Env:  map[string]string{"CLAI_RE_EMPTIED": ""},
		Auth: &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_UNSET_7F3A", "CLAI_RE_EMPTIED"}},
	}
	conn, err := NewStdioConn(t.Context(), server, nil)
	if conn != nil {
		t.Fatal("NewStdioConn returned a Conn alongside a missing variable")
	}
	requireMissing(t, err, "CLAI_RE_EMPTIED", "CLAI_RE_UNSET_7F3A")
	requireNoFile(t, marker)
}

func TestNewStdioConnRequiredEnvSatisfiedByEnvfileAndEnvMap(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	server := pub_models.McpServer{
		Name:    "local",
		Command: "sh",
		Args:    []string{"-c", `[ "$CLAI_RE_FROM_FILE$CLAI_RE_FROM_MAP" = "fm" ] && touch ` + marker},
		EnvFile: writeEnvFile(t, "CLAI_RE_FROM_FILE=f\n"),
		Env:     map[string]string{"CLAI_RE_FROM_MAP": "m"},
		Auth:    &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_FROM_FILE", "CLAI_RE_FROM_MAP"}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conn, err := NewStdioConn(ctx, server, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the spawned process never saw the envfile and env map values it was checked against")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A lazily connected server memoises a missing variable like any other
// run-scoped failure: nothing in the run can set it, so re-dialling on every
// tool call would only repeat the refusal.
func TestConnectorMemoisesMissingRequiredEnv(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	server := pub_models.McpServer{
		Name:    "lazy",
		Command: "sh",
		Args:    []string{"-c", "touch " + marker},
		Auth:    &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_UNSET_7F3A"}},
	}
	var dials int32
	c := newConnector(t.Context(), func(ctx context.Context) (Conn, error) {
		atomic.AddInt32(&dials, 1)
		return dialStdio(ctx, server, nil, mcpConnectBound)
	})
	for i := range 3 {
		_, err := c.Conn(t.Context())
		requireMissing(t, err, "CLAI_RE_UNSET_7F3A")
		if isBlockedOutsideRun(err) {
			t.Fatalf("call %d: a missing variable reported itself blocked outside the run", i)
		}
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("dialled %d times, want 1", got)
	}
	requireNoFile(t, marker)
}

// An endpoint-based server is checked against the process environment
// only: its envfile does not reach auth.token_command, which is what the
// variables are for.
func TestHttpConnectorRefusesMissingRequiredEnvBeforeConnecting(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	server := pub_models.McpServer{
		Name:    "remote",
		Url:     srv.URL,
		EnvFile: writeEnvFile(t, "CLAI_RE_ONLY_IN_FILE=x\n"),
		Auth:    &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_ONLY_IN_FILE"}},
	}
	_, err := NewHttpConnector(t.Context(), server, nil).Conn(t.Context())
	requireMissing(t, err, "CLAI_RE_ONLY_IN_FILE")
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("endpoint received %d requests, want 0", got)
	}
	if conn, err := NewHttpConn(t.Context(), server, nil); conn != nil || err == nil {
		t.Fatalf("NewHttpConn = %v, %v; want no Conn and an error", conn, err)
	}

	t.Setenv("CLAI_RE_ONLY_IN_FILE", "x")
	conn, err := NewHttpConn(t.Context(), server, nil)
	if err != nil {
		t.Fatalf("NewHttpConn with the variable in the process environment: %v", err)
	}
	conn.Close()
}
