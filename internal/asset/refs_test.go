package asset

import (
	"slices"
	"strings"
	"testing"
)

func TestRefsIn(t *testing.T) {
	const a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	const b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.jpg"
	for _, c := range []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{"string content", `{"messages":[{"role":"user","content":"hi"}]}`, nil, ""},
		{"parts without image", `{"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`, nil, ""},
		{"image without asset", `{"messages":[{"role":"user","content":[{"type":"image"}]}]}`, nil, ""},
		{"two images one repeated", `{"messages":[{"content":[{"type":"image","asset":"` + b + `"},{"type":"image","asset":"` + a + `"}]},{"content":[{"type":"image","asset":"` + b + `"}]}]}`, []string{a, b}, ""},
		{"bad content array", `{"messages":[{"content":"x"},{"content":[1,2]}]}`, nil, "message 1"},
		{"no messages", `{}`, nil, ""},
		{"not json", `nope`, nil, "scan"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := RefsIn([]byte(c.in))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
