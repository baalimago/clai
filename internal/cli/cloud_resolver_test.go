package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/baalimago/clai/internal/cloud"
)

func TestCloudResolver_noLoginFailsOnlyWhenAsked(t *testing.T) {
	t.Setenv("CLAI_CLOUD_TOKEN", "")
	t.Setenv("CLAI_CLOUD_URL", "")
	build := cloudResolver(DefaultDeps())

	resolver, err := build(context.Background(), t.TempDir())

	var notLoggedIn *cloud.NotLoggedInError
	if !errors.As(err, &notLoggedIn) || resolver != nil {
		t.Fatalf("resolver %v, err %v; want *cloud.NotLoggedInError and no resolver", resolver, err)
	}
}

func TestCloudResolver_loginFromEnvironment(t *testing.T) {
	t.Setenv("CLAI_CLOUD_TOKEN", "tok")
	t.Setenv("CLAI_CLOUD_URL", "http://127.0.0.1:1")

	resolver, err := cloudResolver(DefaultDeps())(context.Background(), t.TempDir())

	if err != nil || resolver == nil {
		t.Fatalf("resolver %v, err %v; want a resolver", resolver, err)
	}
}

func TestSyncHooks_followTheChatIndex(t *testing.T) {
	hooks := syncHooks(DefaultDeps())

	if hooks.Export == nil || hooks.Pending == nil || hooks.Pulled == nil {
		t.Fatalf("hooks = %+v, want Export, Pending and Pulled set", hooks)
	}
}
