package cloud_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.MainInHome(m) }

func TestClient_errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.WriteHeader(402)
			_, _ = w.Write([]byte(`{"error":"card_required","message":"add a card","url":"https://x/account/"}`))
		case "/plain":
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway\n"))
		case "/empty":
			w.WriteHeader(500)
		case "/garbage":
			_, _ = w.Write([]byte("{"))
		}
	}))
	defer srv.Close()
	c := cloud.NewClient(srv.URL+"/", "t")
	ctx := context.Background()
	err := c.Do(ctx, http.MethodGet, "/json", nil, "", nil)
	apiErr, ok := cloud.AsAPIError(err)
	if !ok || apiErr.Code != "card_required" || apiErr.URL == "" || err.Error() != "add a card" || apiErr.Unauthorized() {
		t.Fatalf("json error = %#v", err)
	}
	err = c.Do(ctx, http.MethodGet, "/plain", nil, "", nil)
	if apiErr, _ := cloud.AsAPIError(err); apiErr.Code != "Bad Gateway" || apiErr.Message != "bad gateway" {
		t.Fatalf("plain error = %#v", err)
	}
	err = c.Do(ctx, http.MethodGet, "/empty", nil, "", nil)
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("empty error = %v", err)
	}
	var out map[string]any
	var te *cloud.TransportError
	if err := c.Do(ctx, http.MethodGet, "/garbage", nil, "", &out); !errors.As(err, &te) || te.Unwrap() == nil {
		t.Fatalf("garbage = %v", err)
	}
	srv.Close()
	if err := c.Do(ctx, http.MethodGet, "/json", nil, "", nil); !errors.As(err, &te) || !strings.Contains(err.Error(), "GET /json") {
		t.Fatalf("offline = %v", err)
	}
	if err := c.Do(ctx, "BAD METHOD", "/x", nil, "", nil); !errors.As(err, &te) {
		t.Fatalf("bad request = %v", err)
	}
	if err := c.PostJSON(ctx, "/x", make(chan int), nil); err == nil {
		t.Fatal("unmarshalable body accepted")
	}
	if _, ok := cloud.AsAPIError(errors.New("x")); ok {
		t.Fatal("AsAPIError on a plain error")
	}
}

func TestClient_meAndRevoke(t *testing.T) {
	srv := cloudtest.New(t)
	c := cloud.NewClient(srv.URL(), "tok-a")
	me, err := c.Me(context.Background())
	if err != nil || me.Name() != "@lorentz" || me.Device != "a" || me.Access != "write" || me.Usage.FreeBytes != 1_000_000_000 {
		t.Fatalf("me = %+v, %v", me, err)
	}
	if (cloud.Me{Account: "u-1"}).Name() != "u-1" {
		t.Fatal("Name without a handle")
	}
	if err := c.RevokeCurrent(context.Background()); err != nil || !srv.Revoked {
		t.Fatalf("revoke: %v", err)
	}
	_, err = c.Me(context.Background())
	if apiErr, ok := cloud.AsAPIError(err); !ok || !apiErr.Unauthorized() {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestCredentials_roundTripAndEnv(t *testing.T) {
	dir := t.TempDir()
	_, err := cloud.LoadCredentials(dir)
	var notIn *cloud.NotLoggedInError
	if !errors.As(err, &notIn) || !strings.Contains(err.Error(), "clai cloud login") {
		t.Fatalf("no login = %v", err)
	}
	want := cloud.Credentials{URL: "https://x", Token: "k", Device: "laptop", Account: "@me", Access: "write"}
	if err := cloud.SaveCredentials(dir, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cloud.Dir(dir) + "/credentials.json")
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
	got, err := cloud.LoadCredentials(dir)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
	t.Setenv("CLAI_CLOUD_TOKEN", "env-token")
	t.Setenv("CLAI_CLOUD_URL", "https://env")
	got, _ = cloud.LoadCredentials(dir)
	if got.Token != "env-token" || got.URL != "https://env" {
		t.Fatalf("env = %+v", got)
	}
	if err := cloud.RemoveCredentials(dir); err != nil {
		t.Fatal(err)
	}
	if err := cloud.RemoveCredentials(dir); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	t.Setenv("CLAI_CLOUD_TOKEN", "")
	t.Setenv("CLAI_CLOUD_URL", "")
	got, err = cloud.LoadCredentials(dir)
	if !errors.As(err, &notIn) || got.URL != cloud.DefaultURL {
		t.Fatalf("after remove = %+v, %v", got, err)
	}
	_ = os.WriteFile(cloud.Dir(dir)+"/credentials.json", []byte("{"), 0o600)
	if _, err := cloud.LoadCredentials(dir); err == nil || errors.As(err, &notIn) {
		t.Fatalf("broken file = %v", err)
	}
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return ctx.Err()
}

func flow(srv *cloudtest.Server, c *clock, shown *cloud.DeviceCode) cloud.LoginFlow {
	return cloud.LoginFlow{
		Client: cloud.NewClient(srv.URL(), ""), Now: c.Now, Sleep: c.Sleep,
		Show: func(code cloud.DeviceCode) error { *shown = code; return nil },
	}
}

func TestLoginFlow(t *testing.T) {
	t.Run("approved after waiting and a slow down", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.PollsLeft, srv.SlowDown = 2, true
		c := &clock{now: time.Unix(0, 0)}
		var shown cloud.DeviceCode
		tok, err := flow(srv, c, &shown).Run(context.Background(), "laptop")
		if err != nil || tok.Key != "tok-new" || tok.Access != "write" || srv.DeviceName != "laptop" {
			t.Fatalf("tok = %+v, %v", tok, err)
		}
		if shown.UserCode != "BCDF-GHJK" || !strings.Contains(shown.VerificationURIComplete, "/cli/login#") {
			t.Fatalf("shown = %+v", shown)
		}
		// 5 s, then 10 s after slow_down, twice more while pending.
		if got := c.now.Sub(time.Unix(0, 0)); got != 35*time.Second {
			t.Fatalf("waited %v", got)
		}
	})
	t.Run("denied", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.Deny = true
		var shown cloud.DeviceCode
		_, err := flow(srv, &clock{}, &shown).Run(context.Background(), "x")
		var le *cloud.LoginError
		if !errors.As(err, &le) || le.Code != "access_denied" || !strings.Contains(err.Error(), "refused") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("expires", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.PollsLeft = 1000
		var shown cloud.DeviceCode
		_, err := flow(srv, &clock{}, &shown).Run(context.Background(), "x")
		var le *cloud.LoginError
		if !errors.As(err, &le) || le.Code != "expired_token" || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("show fails", func(t *testing.T) {
		srv := cloudtest.New(t)
		boom := errors.New("boom")
		f := flow(srv, &clock{}, new(cloud.DeviceCode))
		f.Show = func(cloud.DeviceCode) error { return boom }
		if _, err := f.Run(context.Background(), "x"); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		srv := cloudtest.New(t)
		srv.PollsLeft = 1000
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		f := flow(srv, &clock{}, new(cloud.DeviceCode))
		if _, err := f.Run(ctx, "x"); err == nil {
			t.Fatal("a cancelled login returned no error")
		}
	})
	t.Run("server down", func(t *testing.T) {
		srv := cloudtest.New(t)
		f := flow(srv, &clock{}, new(cloud.DeviceCode))
		srv.Close()
		if _, err := f.Run(context.Background(), "x"); err == nil {
			t.Fatal("no error offline")
		}
	})
}
