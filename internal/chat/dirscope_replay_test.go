package chat

import "testing"

func TestDirscopeReplayQuerier_RawSuppressesCompletionNotification(t *testing.T) {
	if !(dirscopeReplayQuerier{raw: true}).SuppressCompletionNotification() {
		t.Fatal("raw dre must suppress the completion notification")
	}
	if (dirscopeReplayQuerier{raw: false}).SuppressCompletionNotification() {
		t.Fatal("formatted dre must keep the completion notification")
	}
}
