package cloudcmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func offline(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, "tok-a")
	h.srv.Close()
	return h
}

func wantUnreachable(t *testing.T, what string, err error) {
	t.Helper()
	var u *UnreachableError
	if !errors.As(err, &u) {
		t.Fatalf("%s: err = %v, want an UnreachableError", what, err)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "can't reach 127.0.0.1:") || !strings.HasSuffix(msg, "(connection refused)") || strings.Contains(msg, "http://") {
		t.Fatalf("%s: %q", what, msg)
	}
}

func TestUnreachable_isOneShortMessage(t *testing.T) {
	h := offline(t)
	r := &runner{deps: h.deps().withDefaults()}
	ctx := context.Background()
	wantUnreachable(t, "whoami", r.whoami(ctx, false))
	wantUnreachable(t, "pull", r.pull(ctx, nil, false, false, false))
	wantUnreachable(t, "log", r.log(ctx, nil, 5, false))
	h.write("profiles/x.json", "1")
	wantUnreachable(t, "commit", r.commit(ctx, nil, "", false, false))
	wantUnreachable(t, "logout", r.logout(ctx))
	wantUnreachable(t, "login", r.login(ctx, h.srv.URL(), ""))
}

func TestStatus_unreachableIsItsOwnRow(t *testing.T) {
	h := offline(t)
	h.write("profiles/x.json", "1")
	code, out := h.run("status", "config")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	head, _, _ := strings.Cut(out, "\n")
	if strings.Contains(head, "reach") || strings.Contains(out, "dial tcp") || strings.Contains(out, "http://") {
		t.Fatalf("status:\n%s", out)
	}
	mustContain(t, out, "  ! can't reach 127.0.0.1:", "(connection refused)")
}

func TestRevokedToken_saysWhereTheKeyCameFrom(t *testing.T) {
	h := newHarness(t, "revoked")
	r := &runner{deps: h.deps().withDefaults()}
	if err := r.whoami(context.Background(), false); err == nil || strings.Contains(err.Error(), "CLAI_CLOUD_TOKEN") || !strings.Contains(err.Error(), "clai cloud login") {
		t.Fatalf("saved key: err = %v", err)
	}
	t.Setenv("CLAI_CLOUD_TOKEN", "revoked")
	err := r.whoami(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "refused the key in CLAI_CLOUD_TOKEN") || strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("env key: err = %v", err)
	}
}
