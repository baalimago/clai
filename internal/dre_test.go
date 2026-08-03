package internal

import "testing"

func TestDREQuerier_RawSuppressesCompletionNotification(t *testing.T) {
	if !(dreQuerier{raw: true}).SuppressCompletionNotification() {
		t.Fatal("raw dre must suppress the completion notification")
	}
	if (dreQuerier{raw: false}).SuppressCompletionNotification() {
		t.Fatal("formatted dre must keep the completion notification")
	}
}
