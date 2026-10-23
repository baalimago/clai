package tool

import (
	"bufio"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// emojiSpans is exactly emoji-data 15.1's Emoji code points, without keycap bases,
// regional indicators and skin tones, plus U+1F900–1F9FF and U+1FA70–1FAFF.
func TestEmojiSpansMatchUnicodeData(t *testing.T) {
	f, err := os.Open("testdata/emoji-data.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[rune]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		rng, prop, ok := strings.Cut(line, ";")
		if !ok || strings.TrimSpace(prop) != "Emoji" {
			continue
		}
		lo, hi, isRange := strings.Cut(strings.TrimSpace(rng), "..")
		if !isRange {
			hi = lo
		}
		a, errA := strconv.ParseInt(lo, 16, 32)
		b, errB := strconv.ParseInt(hi, 16, 32)
		if errA != nil || errB != nil {
			t.Fatalf("bad range %q", rng)
		}
		for r := rune(a); r <= rune(b); r++ {
			want[r] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for r := rune(0x1f900); r <= 0x1faff; r++ {
		if r <= 0x1f9ff || r >= 0x1fa70 {
			want[r] = true
		}
	}
	for r := range want {
		if isKeycapBase(r) || isRegionalIndicator(r) || isSkinTone(r) {
			delete(want, r)
		}
	}
	got := map[rune]bool{}
	for _, span := range emojiSpans() {
		for r := span[0]; r <= span[1]; r++ {
			got[r] = true
		}
	}
	var missing, extra []rune
	for r := range want {
		if !got[r] {
			missing = append(missing, r)
		}
	}
	for r := range got {
		if !want[r] {
			extra = append(extra, r)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		slices.Sort(missing)
		slices.Sort(extra)
		t.Fatalf("emojiSpans differs from the data: missing %U, extra %U", missing, extra)
	}
}
