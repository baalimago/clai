package sync

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

// A reset of one store leaves the other store's state, base copies and
// conflict copies alone.
func TestResetStoreState_leavesTheOtherStoresCopies(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "one\n")
	a.write("conversations/c1.json", `{"messages":[{"role":"user","content":"x"}]}`)
	a.commit(StoreConfig, "settings")
	a.commit(StoreConversations, "chats")
	// Each store's side of a conflict, and the config store's base copy.
	a.write("cloud/theirs/profiles/x.json", "conflict\n")
	a.write("cloud/theirs/conversations/c1.json", "conflict")
	if !a.exists("cloud/base/profiles/x.json") {
		t.Fatal("a commit wrote no base copy")
	}
	if err := ResetStoreState(a.ConfDir, StoreConfig); err != nil {
		t.Fatal(err)
	}
	if a.exists("cloud/base/profiles/x.json") || a.exists("cloud/theirs/profiles/x.json") {
		t.Fatal("reset kept the store's own copies")
	}
	if !a.exists("cloud/theirs/conversations/c1.json") {
		t.Fatal("reset took the other store's conflict copy")
	}
	a.pull(StoreConfig, PullOptions{})
	if err := ResetStoreState(a.ConfDir, StoreConversations); err != nil {
		t.Fatal(err)
	}
	if a.exists("cloud/theirs/conversations/c1.json") {
		t.Fatal("reset kept the store's own conflict copy")
	}
	if _, err := os.Stat(statePath(a.ConfDir, StoreConfig)); err != nil {
		t.Fatalf("the second reset took the first store's state: %v", err)
	}
}

// A path this machine deleted is its own change, so a pull leaves it
// deleted; a reset forgets that, and the account's copy comes back.
func TestResetStoreState_takesTheAccountsCopy(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "one\n")
	a.write("conversations/c1.json", `{"messages":[{"role":"user","content":"x"}]}`)
	a.commit(StoreConfig, "settings")
	a.commit(StoreConversations, "chats")
	if err := os.Remove(a.abs("profiles/x.json")); err != nil {
		t.Fatal(err)
	}
	if a.pull(StoreConfig, PullOptions{}).Added != 0 || a.exists("profiles/x.json") {
		t.Fatal("a pull restored a local delete")
	}
	if err := ResetStoreState(a.ConfDir, StoreConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath(a.ConfDir, StoreConversations)); err != nil {
		t.Fatalf("reset took the other store's state: %v", err)
	}
	if res := a.pull(StoreConfig, PullOptions{}); res.Added != 1 || a.read("profiles/x.json") != "one\n" {
		t.Fatalf("pull after reset: added %d", res.Added)
	}
	// A change of this machine's loses to the account's too, into the trash.
	a.write("profiles/x.json", "mine\n")
	if err := ResetStoreState(a.ConfDir, StoreConfig); err != nil {
		t.Fatal(err)
	}
	res := a.pull(StoreConfig, PullOptions{Mode: ModeTheirs})
	if a.read("profiles/x.json") != "one\n" || len(res.Trashed) != 1 {
		t.Fatalf("reset kept a local edit: %q trashed %v", a.read("profiles/x.json"), res.Trashed)
	}
}

// ResetState still forgets both stores, as login and logout need.
func TestResetState_forgetsEveryStore(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "one\n")
	a.write("conversations/c1.json", `{"messages":[{"role":"user","content":"x"}]}`)
	a.commit(StoreConfig, "settings")
	a.commit(StoreConversations, "chats")
	if err := ResetState(a.ConfDir); err != nil {
		t.Fatal(err)
	}
	for _, store := range Stores {
		if _, err := os.Stat(statePath(a.ConfDir, store)); !os.IsNotExist(err) {
			t.Fatalf("%s state survived: %v", store, err)
		}
	}
	if a.pull(StoreConfig, PullOptions{}); a.read("profiles/x.json") != "one\n" {
		t.Fatalf("pull after a full reset = %q", a.read("profiles/x.json"))
	}
}
