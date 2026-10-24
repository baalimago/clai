#!/usr/bin/env bash
set -euo pipefail

demo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${1:-$HOME/.cache/clai-demo}"
home="$root/home"

rm -rf "$root"
mkdir -p "$home/.config/.clai/conversations/dirs" "$home/work"
cp "$demo_dir"/conversations/*.json "$home/.config/.clai/conversations/"
cp "$demo_dir/clai-shell.sh" "$home/clai-shell.sh"
cp "$demo_dir/classify.json" "$home/work/classify.json"
# Pin the model and skip summary generation: the fixture chats carry titles,
# and a background summary would rebuild the chat index mid-recording.
printf '%s\n' '{"model":"claude-sonnet-5-5","summarize-conversations":false,"use-tools":false}' \
	> "$home/.config/.clai/textConfig.json"
install -m 755 "$demo_dir/note.sh" /tmp/clai-note.sh

cd "$home/work"
cat > go.mod <<'EOF'
module demo

go 1.23
EOF

cat > demo.go <<'EOF'
package demo

// Add returns the sum of a and b.
func Add(a, b int) int {
	return 0
}
EOF

cat > demo_test.go <<'EOF'
package demo

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(1, 2); got != 3 {
		t.Fatalf("Add(1, 2) = %d, want 3", got)
	}
}
EOF

# Warm the Go build cache so the recorded `go test` is not a cold start, then
# complete the config migration and build the chat index while hidden. The
# future window matches no chat and therefore calls no model.
( HOME="$home" go test ./... >/dev/null 2>&1 ) || true
( HOME="$home" CLAI_CONFIG_DIR="$home/.config/.clai" CLAI_CACHE_DIR="$home/.cache/clai" \
	clai c summarize 2099-01-01 >/dev/null 2>&1 ) || true
