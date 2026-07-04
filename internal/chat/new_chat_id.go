package chat

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewChatID generates a collision-resistant chat ID suitable for cloning
// foreign conversations.
//
// Format: <unix_seconds_hex>-<random_10_bytes_hex>
// Example: 66f3a2b1-9f6c3b2a1c0d4e5f6a7b
//
// It avoids HashIDFromPrompt collisions and does not inject wall-clock time
// into the chat payload itself (only the identifier).
func NewChatID() string {
	b := make([]byte, 10)
	_, err := rand.Read(b)
	if err != nil {
		// Extremely unlikely; fall back to time-based entropy.
		return fmt.Sprintf("%x-%x", time.Now().Unix(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%s", time.Now().Unix(), hex.EncodeToString(b))
}
